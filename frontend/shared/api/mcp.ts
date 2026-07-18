import { authedFetch, authedRequest } from "@/shared/api/authed-client";
import type { FileContentResult } from "@/shared/api/file";
import { pathParam } from "@/shared/api/http-client";
import type { MCPToolDTO, MCPToolListResponse } from "@/shared/api/mcp.types";

export async function listAvailableMCPTools(accessToken: string): Promise<MCPToolDTO[]> {
  const data = await authedRequest<MCPToolListResponse>(
    "/api/v1/mcp/tools",
    {
      method: "GET",
      accessToken,
    },
    true,
  );
  return data.results ?? [];
}

export async function fetchRAGFlowDocumentPreview(
  accessToken: string,
  documentID: string,
): Promise<FileContentResult> {
  const response = await authedFetch(
    `/api/v1/mcp/ragflow/documents/${pathParam(documentID)}/preview`,
    {
      method: "GET",
      accessToken,
      cache: "no-store",
    },
    true,
  );
  const blob = await response.blob();
  const rawContentLength = response.headers.get("content-length");
  const parsedContentLength = rawContentLength ? Number.parseInt(rawContentLength, 10) : Number.NaN;
  return {
    blob,
    contentType: response.headers.get("content-type") || blob.type || "application/octet-stream",
    disposition: response.headers.get("content-disposition"),
    contentLength: Number.isFinite(parsedContentLength) ? parsedContentLength : blob.size || null,
  };
}
