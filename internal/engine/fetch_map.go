package engine

import (
	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/internal/httpx"
)

func mapHTTPCode(code string) (crawl.FetchOutcome, crawl.ErrorCode) {
	switch code {
	case "":
		return crawl.FetchOutcomeHTTPResponse, crawl.CodeNone
	case "method_denied":
		return crawl.FetchOutcomePolicyDenied, crawl.CodeMethodDenied
	case "policy_denied":
		return crawl.FetchOutcomePolicyDenied, crawl.CodePolicyDenied
	case "address_denied":
		return crawl.FetchOutcomeAddressDenied, crawl.CodeAddressDenied
	case "dns_failed":
		return crawl.FetchOutcomeDNSFailed, crawl.CodeDNSFailed
	case "connect_failed":
		return crawl.FetchOutcomeConnectFailed, crawl.CodeConnectFailed
	case "tls_failed":
		return crawl.FetchOutcomeTLSFailed, crawl.CodeTLSFailed
	case "request_timeout":
		return crawl.FetchOutcomeTimedOut, crawl.CodeRequestTimeout
	case "cancelled":
		return crawl.FetchOutcomeCancelled, crawl.CodeCancelled
	case "header_too_large":
		return crawl.FetchOutcomeHeaderTooLarge, crawl.CodeHeaderTooLarge
	case "wire_body_too_large":
		return crawl.FetchOutcomeWireBodyTooLarge, crawl.CodeWireBodyTooLarge
	case "decoded_body_too_large":
		return crawl.FetchOutcomeDecodedBodyTooLarge, crawl.CodeDecodedBodyTooLarge
	case "converted_text_too_large":
		return crawl.FetchOutcomeConvertedTextTooLarge, crawl.CodeConvertedTextTooLarge
	case "content_decode_failed":
		return crawl.FetchOutcomeContentDecodeFailed, crawl.CodeContentDecodeFailed
	case "unsupported_content_encoding":
		return crawl.FetchOutcomeContentDecodeFailed, crawl.CodeUnsupportedEncoding
	case "protocol_failed":
		return crawl.FetchOutcomeProtocolFailed, crawl.CodeProtocolFailed
	default:
		return crawl.FetchOutcomeTransportFailed, crawl.CodeTransportFailed
	}
}

func fetchResultFromHTTP(resp httpx.Response, finalURL string) crawl.FetchResult {
	out, code := mapHTTPCode(resp.Code)
	if out != crawl.FetchOutcomeHTTPResponse {
		r := crawl.NewFetchResult(out, code, finalURL, resp.Duration)
		return r.WithFetchMetrics(resp.WireBytes, resp.DecodedBytes)
	}
	httpResp := crawl.NewHTTPResponse(resp.StatusCode, resp.Headers, resp.Body)
	return crawl.NewHTTPFetchResult(httpResp, finalURL, resp.WireBytes, resp.DecodedBytes, resp.Duration)
}
