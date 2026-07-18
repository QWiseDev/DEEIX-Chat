"use client";

import * as React from "react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";

const DINGTALK_QR_SDK_ID = "dingtalk-qr-login-sdk";
const DINGTALK_QR_SDK_URL = "https://g.alicdn.com/dingding/h5-dingtalk-login/0.21.0/ddlogin.js";

type DingTalkQRCodeResult = {
  authCode?: string;
  state?: string;
};

type DingTalkFrameLogin = (
  frameParams: { id: string; width: number; height: number },
  loginParams: {
    redirect_uri: string;
    client_id: string;
    scope: string;
    response_type: "code";
    state: string;
    prompt: "consent";
  },
  success: (result: DingTalkQRCodeResult) => void,
  failure: (message: string) => void,
) => void;

let dingTalkQRCodeSDKPromise: Promise<void> | null = null;

function resolveDingTalkFrameLogin(): DingTalkFrameLogin | undefined {
  return (window as Window & { DTFrameLogin?: DingTalkFrameLogin }).DTFrameLogin;
}

function loadDingTalkQRCodeSDK(): Promise<void> {
  if (resolveDingTalkFrameLogin()) {
    return Promise.resolve();
  }
  if (dingTalkQRCodeSDKPromise) {
    return dingTalkQRCodeSDKPromise;
  }
  dingTalkQRCodeSDKPromise = new Promise<void>((resolve, reject) => {
    document.getElementById(DINGTALK_QR_SDK_ID)?.remove();
    const script = document.createElement("script");
    script.id = DINGTALK_QR_SDK_ID;
    script.src = DINGTALK_QR_SDK_URL;
    script.async = true;
    script.onload = () => resolve();
    script.onerror = () => {
      dingTalkQRCodeSDKPromise = null;
      reject(new Error("failed to load DingTalk QR login SDK"));
    };
    document.head.appendChild(script);
  });
  return dingTalkQRCodeSDKPromise;
}

export function DingTalkQRCodeLoginDialog({
  authURL,
  providerName,
  onClose,
}: {
  authURL: string;
  providerName: string;
  onClose: () => void;
}) {
  const t = useTranslations("login.dingtalkQRCode");
  const reactID = React.useId();
  const containerID = React.useMemo(() => `dingtalk-qr-${reactID.replaceAll(":", "")}`, [reactID]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");

  React.useEffect(() => {
    let cancelled = false;
    const container = document.getElementById(containerID);
    container?.replaceChildren();
    setLoading(true);
    setError("");

    void loadDingTalkQRCodeSDK()
      .then(() => {
        if (cancelled) {
          return;
        }
        const parsed = new URL(authURL);
        const redirectURI = parsed.searchParams.get("redirect_uri") ?? "";
        const clientID = parsed.searchParams.get("client_id") ?? "";
        const scope = parsed.searchParams.get("scope") ?? "openid";
        const state = parsed.searchParams.get("state") ?? "";
        const frameLogin = resolveDingTalkFrameLogin();
        if (!redirectURI || !clientID || !state || !frameLogin) {
          dingTalkQRCodeSDKPromise = null;
          throw new Error("invalid DingTalk QR login configuration");
        }
        frameLogin(
          { id: containerID, width: 280, height: 280 },
          {
            redirect_uri: encodeURIComponent(redirectURI),
            client_id: clientID,
            scope,
            response_type: "code",
            state,
            prompt: "consent",
          },
          (result) => {
            if (cancelled) {
              return;
            }
            if (!result.authCode) {
              setError(t("failed"));
              return;
            }
            const callbackURL = new URL(redirectURI);
            callbackURL.searchParams.set("authCode", result.authCode);
            callbackURL.searchParams.set("state", result.state || state);
            window.location.assign(callbackURL.toString());
          },
          () => {
            if (!cancelled) {
              setError(t("failed"));
            }
          },
        );
        setLoading(false);
      })
      .catch(() => {
        if (!cancelled) {
          setLoading(false);
          setError(t("loadFailed"));
        }
      });

    return () => {
      cancelled = true;
      document.getElementById(containerID)?.replaceChildren();
    };
  }, [authURL, containerID, t]);

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="w-[336px] max-w-[calc(100%-1rem)] gap-3 p-3 sm:max-w-[336px]">
        <DialogHeader>
          <DialogTitle>{t("title", { provider: providerName })}</DialogTitle>
          <DialogDescription>{t("description")}</DialogDescription>
        </DialogHeader>
        <div className="relative mx-auto grid size-[280px] max-w-full place-items-center overflow-hidden rounded-lg bg-white" aria-live="polite">
          <div id={containerID} className="size-[280px] max-w-full" />
          {loading ? (
            <div className="absolute grid size-[280px] max-w-full place-items-center rounded-lg bg-white text-slate-600">
              <Spinner className="size-5" label={t("loading")} />
            </div>
          ) : null}
          {error ? (
            <div className="absolute grid size-[280px] max-w-full place-items-center rounded-lg bg-white px-6 text-center text-xs leading-5 text-destructive" role="alert">
              {error}
            </div>
          ) : null}
        </div>
        <DialogFooter className="justify-center sm:justify-center">
          <Button asChild variant="secondary">
            <a href={authURL}>{t("openPage")}</a>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
