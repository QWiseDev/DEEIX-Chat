package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/conv"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/secretbox"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
	"github.com/google/uuid"
)

const dingTalkWorkbenchFlow = "dingtalk_workbench"

type DingTalkWorkbenchStart struct {
	ClientID string
	State    string
}

type DingTalkQRCodeStart struct {
	AuthURL string
}

type dingTalkCachedAppToken struct {
	AccessToken string
	ExpiresAt   time.Time
}

type dingTalkUserTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpireIn     int    `json:"expireIn"`
	CorpID       string `json:"corpId"`
}

type dingTalkAppTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpiresIn   int    `json:"expireIn"`
}

func (s *Service) buildDingTalkOAuthURL(provider domainuser.IdentityProvider, redirectURI string, state string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(s.dingTalkLoginURL))
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	values.Set("redirect_uri", redirectURI)
	values.Set("response_type", "code")
	values.Set("client_id", provider.ClientID)
	values.Set("scope", firstNonEmpty(provider.Scopes, "openid"))
	values.Set("state", state)
	values.Set("prompt", "consent")
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func (s *Service) StartDingTalkQRCodeLogin(ctx context.Context, slug string, redirectURI string, nextPath string, codeChallenge string) (*DingTalkQRCodeStart, error) {
	provider, err := s.repo.GetIdentityProviderBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if provider.Type != domainuser.IdentityProviderTypeDingTalk {
		return nil, fmt.Errorf("provider must be dingtalk")
	}
	target, err := s.BuildProviderAuthURL(ctx, slug, redirectURI, nextPath, codeChallenge, providerIntentLogin)
	if err != nil {
		return nil, err
	}
	return &DingTalkQRCodeStart{AuthURL: target}, nil
}

func (s *Service) StartDingTalkWorkbenchLogin(ctx context.Context, slug string, codeChallenge string) (*DingTalkWorkbenchStart, error) {
	if !s.cfg.Snapshot().ThirdPartyLoginEnabled {
		return nil, fmt.Errorf("third-party login is disabled")
	}
	if err := validateProviderCodeChallenge(codeChallenge); err != nil {
		return nil, err
	}
	provider, err := s.repo.GetIdentityProviderBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if provider.Type != domainuser.IdentityProviderTypeDingTalk {
		return nil, fmt.Errorf("provider must be dingtalk")
	}
	if !provider.LoginEnabled {
		return nil, fmt.Errorf("provider login is disabled")
	}
	state, err := s.signProviderState(providerOAuthState{
		Provider:      provider.Slug,
		Intent:        providerIntentLogin,
		Flow:          dingTalkWorkbenchFlow,
		CodeChallenge: strings.TrimSpace(codeChallenge),
		Nonce:         conv.NormalizePublicID(uuid.NewString()),
		ExpiresAt:     time.Now().Add(10 * time.Minute).Unix(),
	})
	if err != nil {
		return nil, err
	}
	return &DingTalkWorkbenchStart{ClientID: provider.ClientID, State: state}, nil
}

func (s *Service) CompleteDingTalkWorkbenchLogin(
	ctx context.Context,
	slug string,
	code string,
	corpID string,
	state string,
	codeVerifier string,
	requestID string,
	auditCtx requestmeta.SessionAuditContext,
) (*LoginResult, error) {
	if !s.cfg.Snapshot().ThirdPartyLoginEnabled {
		return nil, fmt.Errorf("third-party login is disabled")
	}
	trimmedCode := strings.TrimSpace(code)
	if trimmedCode == "" {
		return nil, fmt.Errorf("authorization code is required")
	}
	trimmedCorpID := strings.TrimSpace(corpID)
	if trimmedCorpID == "" {
		return nil, fmt.Errorf("dingtalk corp id is required")
	}
	provider, err := s.repo.GetIdentityProviderBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if provider.Type != domainuser.IdentityProviderTypeDingTalk {
		return nil, fmt.Errorf("provider must be dingtalk")
	}
	if !provider.LoginEnabled {
		return nil, fmt.Errorf("provider login is disabled")
	}
	verifiedState, err := s.verifySignedProviderState(state)
	if err != nil {
		return nil, err
	}
	if verifiedState.Provider != provider.Slug || verifiedState.Flow != dingTalkWorkbenchFlow || verifiedState.Intent != providerIntentLogin {
		return nil, fmt.Errorf("oauth state mismatch")
	}
	if err = validateProviderCodeVerifier(codeVerifier, verifiedState.CodeChallenge); err != nil {
		return nil, err
	}

	appToken, err := s.getDingTalkAppToken(ctx, *provider)
	if err != nil {
		return nil, err
	}
	identity, err := s.fetchDingTalkWorkbenchIdentity(ctx, appToken, trimmedCode)
	if err != nil {
		return nil, err
	}
	userID := claimString(identity, "userid")
	if userID == "" {
		return nil, fmt.Errorf("dingtalk user is not an enterprise member")
	}
	directory, err := s.fetchDingTalkDirectoryProfile(ctx, appToken, userID)
	if err != nil {
		return nil, err
	}
	profile, err := normalizeDingTalkProfile(trimmedCorpID, userID, identity, directory, nil)
	if err != nil {
		return nil, err
	}
	return s.completeProviderProfileLogin(ctx, *provider, profile, providerIntentLogin, requestID, auditCtx)
}

func (s *Service) fetchDingTalkOAuthProfile(ctx context.Context, provider domainuser.IdentityProvider, code string) (map[string]interface{}, error) {
	userToken, err := s.exchangeDingTalkUserToken(ctx, provider, code)
	if err != nil {
		return nil, err
	}
	corpID := strings.TrimSpace(userToken.CorpID)
	personal, err := s.fetchDingTalkPersonalProfile(ctx, userToken.AccessToken)
	if err != nil {
		return nil, err
	}
	unionID := claimString(personal, "unionId")
	if unionID == "" {
		return nil, fmt.Errorf("provider subject is missing")
	}
	appToken, err := s.getDingTalkAppToken(ctx, provider)
	if err != nil {
		return nil, err
	}
	userID, err := s.resolveDingTalkUserID(ctx, appToken, unionID)
	if err != nil {
		return nil, err
	}
	directory, err := s.fetchDingTalkDirectoryProfile(ctx, appToken, userID)
	if err != nil {
		return nil, err
	}
	return normalizeDingTalkProfile(corpID, userID, nil, directory, personal)
}

func (s *Service) exchangeDingTalkUserToken(ctx context.Context, provider domainuser.IdentityProvider, code string) (*dingTalkUserTokenResponse, error) {
	clientSecret, err := secretbox.DecryptString(s.cfg.Snapshot().DataEncryptionKey, provider.ClientSecret)
	if err != nil {
		return nil, err
	}
	payload := map[string]string{
		"clientId":     provider.ClientID,
		"clientSecret": clientSecret,
		"code":         strings.TrimSpace(code),
		"grantType":    "authorization_code",
	}
	var response dingTalkUserTokenResponse
	if err = s.doDingTalkJSON(ctx, http.MethodPost, s.dingTalkAPIBaseURL+"/v1.0/oauth2/userAccessToken", "", payload, &response); err != nil {
		return nil, err
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return nil, fmt.Errorf("provider token response missing access token")
	}
	return &response, nil
}

func (s *Service) fetchDingTalkPersonalProfile(ctx context.Context, accessToken string) (map[string]interface{}, error) {
	var profile map[string]interface{}
	if err := s.doDingTalkJSON(ctx, http.MethodGet, s.dingTalkAPIBaseURL+"/v1.0/contact/users/me", accessToken, nil, &profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *Service) getDingTalkAppToken(ctx context.Context, provider domainuser.IdentityProvider) (string, error) {
	cacheKey := fmt.Sprintf("%d\x00%s", provider.ID, provider.ClientID)
	s.dingTalkTokenMu.Lock()
	defer s.dingTalkTokenMu.Unlock()
	if cached, ok := s.dingTalkAppTokens[cacheKey]; ok && cached.AccessToken != "" && time.Now().Before(cached.ExpiresAt) {
		return cached.AccessToken, nil
	}
	clientSecret, err := secretbox.DecryptString(s.cfg.Snapshot().DataEncryptionKey, provider.ClientSecret)
	if err != nil {
		return "", err
	}
	payload := map[string]string{
		"appKey":    provider.ClientID,
		"appSecret": clientSecret,
	}
	endpoint := s.dingTalkAPIBaseURL + "/v1.0/oauth2/accessToken"
	var response dingTalkAppTokenResponse
	if err = s.doDingTalkJSON(ctx, http.MethodPost, endpoint, "", payload, &response); err != nil {
		return "", err
	}
	accessToken := strings.TrimSpace(response.AccessToken)
	if accessToken == "" {
		return "", fmt.Errorf("provider token response missing access token")
	}
	expiresIn := response.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 7200
	}
	cacheTTL := time.Duration(expiresIn)*time.Second - time.Minute
	if cacheTTL <= 0 {
		cacheTTL = time.Minute
	}
	s.dingTalkAppTokens[cacheKey] = dingTalkCachedAppToken{AccessToken: accessToken, ExpiresAt: time.Now().Add(cacheTTL)}
	return accessToken, nil
}

func (s *Service) fetchDingTalkWorkbenchIdentity(ctx context.Context, accessToken string, code string) (map[string]interface{}, error) {
	return s.postDingTalkForm(ctx, s.dingTalkOAPIBaseURL+"/topapi/v2/user/getuserinfo", url.Values{
		"access_token": {accessToken},
		"code":         {strings.TrimSpace(code)},
	})
}

func (s *Service) resolveDingTalkUserID(ctx context.Context, accessToken string, unionID string) (string, error) {
	result, err := s.postDingTalkForm(ctx, s.dingTalkOAPIBaseURL+"/topapi/user/getbyunionid", url.Values{
		"access_token": {accessToken},
		"unionid":      {strings.TrimSpace(unionID)},
	})
	if err != nil {
		return "", err
	}
	userID := claimString(result, "userid")
	if userID == "" {
		return "", fmt.Errorf("dingtalk user is not an enterprise member")
	}
	return userID, nil
}

func (s *Service) fetchDingTalkDirectoryProfile(ctx context.Context, accessToken string, userID string) (map[string]interface{}, error) {
	return s.postDingTalkForm(ctx, s.dingTalkOAPIBaseURL+"/topapi/v2/user/get", url.Values{
		"access_token": {accessToken},
		"userid":       {strings.TrimSpace(userID)},
		"language":     {"zh_CN"},
	})
}

func (s *Service) postDingTalkForm(ctx context.Context, endpoint string, form url.Values) (map[string]interface{}, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	response, err := s.providerHTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("provider userinfo failed: %s", response.Status)
	}
	var payload map[string]interface{}
	if err = json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if code := claimString(payload, "errcode"); code != "" && code != "0" {
		return nil, fmt.Errorf("provider userinfo failed: %s", firstNonEmpty(claimString(payload, "errmsg"), code))
	}
	result, ok := payload["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("provider userinfo failed: missing result")
	}
	return result, nil
}

func (s *Service) doDingTalkJSON(ctx context.Context, method string, endpoint string, accessToken string, payload interface{}, output interface{}) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(accessToken) != "" {
		request.Header.Set("x-acs-dingtalk-access-token", strings.TrimSpace(accessToken))
	}
	response, err := s.providerHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("provider token exchange failed: %s", response.Status)
	}
	if err = json.Unmarshal(responseBody, output); err != nil {
		return err
	}
	return nil
}

func normalizeDingTalkProfile(
	corpID string,
	userID string,
	workbench map[string]interface{},
	directory map[string]interface{},
	personal map[string]interface{},
) (map[string]interface{}, error) {
	workbenchUnionID := claimString(workbench, "unionid")
	directoryUnionID := claimString(directory, "unionid")
	personalUnionID := claimString(personal, "unionId")
	unionID := firstNonEmpty(directoryUnionID, workbenchUnionID, personalUnionID)
	if unionID == "" {
		return nil, fmt.Errorf("provider subject is missing")
	}
	for _, candidate := range []string{workbenchUnionID, directoryUnionID, personalUnionID} {
		if candidate != "" && candidate != unionID {
			return nil, fmt.Errorf("provider authentication failed")
		}
	}
	email := firstNonEmpty(claimString(directory, "org_email"), claimString(directory, "email"))
	profile := map[string]interface{}{
		"unionId":       unionID,
		"userId":        strings.TrimSpace(userID),
		"corpId":        strings.TrimSpace(corpID),
		"name":          firstNonEmpty(claimString(directory, "name"), claimString(workbench, "name"), claimString(personal, "nick")),
		"avatarURL":     firstNonEmpty(claimString(directory, "avatar"), claimString(personal, "avatarUrl")),
		"email":         email,
		"emailVerified": email != "",
		"active":        claimBool(directory, "active"),
		"raw": map[string]interface{}{
			"workbench": workbench,
			"directory": directory,
			"personal":  personal,
		},
	}
	return profile, nil
}
