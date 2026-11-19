# MCP transports in Relay 0.3.0

| Capability | stdio | Streamable HTTP |
|---|---:|---:|
| JSON-RPC initialize / initialized | Yes | Yes |
| tools/list pagination | Yes | Yes |
| tools/call | Yes | Yes |
| OAuth discovery from HTTP 401 | No | Yes |
| Relay Builder OAuth token integration | Yes, explicit OAuth block | Not needed for remote server |
| Session ID lifecycle | N/A | Yes |
| MCP protocol-version header | N/A | Yes |
| JSON response | N/A | Yes |
| SSE response to POST | N/A | Yes |
| OAuth resource/audience binding | Token-file scoped by server ID | Exact resource indicator checked before bearer attachment |
| Redirect following | N/A | No |
| Legacy HTTP+SSE transport | N/A | No |
| Client-initiated GET SSE stream | N/A | No |

## Protocol version target

The current MCP client targets the `2025-06-18` protocol version and also accepts `2025-03-26` as a legacy compatibility version. It does not claim support for `2025-11-25` or the newer stateless `2026-07-28` protocol revision. The HTTP authorization behavior is based on the MCP `2025-06-18` authorization profile (RFC 9728, RFC 8414, RFC 7591 and RFC 8707).

## Streamable HTTP details

Relay sends JSON-RPC messages with HTTP POST and accepts a JSON response or an SSE stream containing the matching JSON-RPC response. It preserves `Mcp-Session-Id` after initialization and sends `MCP-Protocol-Version` after negotiation. HTTP 401 responses are converted to a structured auth-required result; the `WWW-Authenticate` `resource_metadata` URL is used for protected-resource discovery. Redirects are not followed. A bearer token is attached only when the stored resource identity matches and the exact MCP target URL is unchanged; discovery verifies that the resource URI covers the endpoint. HTTP MCP always sends the canonical RFC 8707 `resource` parameter in both authorization and token requests.

OAuth metadata/token HTTP requests use a direct resolver-aware dialer, reject non-public DNS answers for remote OAuth endpoints and never follow redirects. The configured MCP destination itself remains a user-selected endpoint; redirects are refused before credentials can be forwarded.

## stdio details

stdio runs a local process with a controlled environment. Relay does not infer OAuth from arbitrary error text. Builder-generated servers can opt into the explicit token-file contract and use the bundled Python SDK. Only the SDK's auth-required marker is accepted, and only when the server has an explicit OAuth block. The connector receives the access-token handoff file, not the refresh token or client secret.

## Scope

This is the implemented support matrix, not a claim of complete MCP transport conformance. See [LFI_CORE.md](LFI_CORE.md) for remaining release gates and [OAUTH.md](OAUTH.md) for provider/storage details.
