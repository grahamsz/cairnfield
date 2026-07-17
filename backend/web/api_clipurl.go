package web

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	maxClipURLBytes     = 10 << 20
	clipURLFetchTimeout = 15 * time.Second
	clipURLMaxRedirects = 5
	clipURLUserAgent    = "cairnfield/dev (+notes-app)"
)

var (
	// errClipURLBlocked marks requests rejected by the SSRF policy (bad scheme,
	// credentials, fragments, unresolvable or disallowed hosts). It maps to 400.
	errClipURLBlocked = errors.New("url is not allowed")
	errClipTooLarge   = errors.New("page exceeds the size limit")
	errClipNotHTML    = errors.New("URL did not return HTML")

	clipURLTitleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

	// clipURLAllowPrivateHook lets tests dial private addresses (httptest
	// servers) by host:port. It stays nil in production.
	clipURLAllowPrivateHook func(addr string) bool
)

func (s *Server) apiClipURL(w http.ResponseWriter, r *http.Request) {
	cu := current(r)
	if cu.User.ID == 0 {
		var ok bool
		cu, ok = s.requireBearerAuth(w, r)
		if !ok {
			return
		}
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.blobs == nil {
		writeAPIError(w, http.StatusInternalServerError, "blob storage is not configured")
		return
	}
	var body struct {
		URL        string `json:"url"`
		FolderPath string `json:"folder_path"`
		Title      string `json:"title"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	finalURL, data, err := fetchClipURLPage(r.Context(), body.URL)
	if err != nil {
		switch {
		case errors.Is(err, errClipURLBlocked):
			writeAPIError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, errClipTooLarge):
			writeAPIError(w, http.StatusRequestEntityTooLarge, err.Error())
		case errors.Is(err, errClipNotHTML):
			writeAPIError(w, http.StatusBadRequest, err.Error())
		default:
			writeAPIError(w, http.StatusBadGateway, "failed to fetch URL: "+err.Error())
		}
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = extractHTMLTitle(data)
	}
	capturedAt := time.Now().UTC()
	meta := clipMetadata{
		Title:           title,
		SourceURL:       finalURL,
		PageURL:         finalURL,
		FolderPath:      body.FolderPath,
		DestinationKind: "folder",
		CapturedAt:      capturedAt.Format(time.RFC3339),
	}
	note, version, asset, err := s.createHTMLClip(r.Context(), cu.User.ID, meta, "clip.html", data, nil)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"note": note, "version": version, "asset": asset, "url": s.appPath(fmt.Sprintf("/notes/%s", note.Slug))})
}

// fetchClipURLPage downloads a web page with SSRF guards: only plain http(s)
// URLs, no credentials or fragments, and every resolved address (initial host,
// each redirect target, and the actual dial) must be public.
func fetchClipURLPage(ctx context.Context, rawURL string) (string, []byte, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", nil, fmt.Errorf("%w: invalid URL", errClipURLBlocked)
	}
	ctx, cancel := context.WithTimeout(ctx, clipURLFetchTimeout)
	defer cancel()
	if err := validateClipFetchURL(ctx, u); err != nil {
		return "", nil, err
	}
	transport := &http.Transport{
		DialContext:         clipURLDialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        4,
		IdleConnTimeout:     5 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > clipURLMaxRedirects {
				return errors.New("too many redirects")
			}
			return validateClipFetchURL(req.Context(), req.URL)
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", nil, fmt.Errorf("%w: invalid URL", errClipURLBlocked)
	}
	req.Header.Set("User-Agent", clipURLUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errClipURLBlocked) {
			return "", nil, err
		}
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("page returned status %d", resp.StatusCode)
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") {
		return "", nil, errClipNotHTML
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxClipURLBytes+1))
	if err != nil {
		return "", nil, err
	}
	if len(data) > maxClipURLBytes {
		return "", nil, errClipTooLarge
	}
	return resp.Request.URL.String(), data, nil
}

// validateClipFetchURL enforces the URL-level SSRF policy and resolves the
// host so obviously disallowed targets fail before any connection is made.
func validateClipFetchURL(ctx context.Context, u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%w: only http and https URLs are supported", errClipURLBlocked)
	}
	if u.User != nil {
		return fmt.Errorf("%w: URLs with credentials are not allowed", errClipURLBlocked)
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return fmt.Errorf("%w: URLs with fragments are not allowed", errClipURLBlocked)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: host is required", errClipURLBlocked)
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("%w: host cannot be resolved", errClipURLBlocked)
	}
	addr := clipURLAddr(host, u)
	for _, ip := range ips {
		if !clipURLIPAllowed(ip, addr) {
			return fmt.Errorf("%w: host resolves to a disallowed address", errClipURLBlocked)
		}
	}
	return nil
}

// clipURLDialContext pins DNS at dial time: the host is resolved once, every
// address is re-checked against the SSRF policy, and only validated addresses
// are dialed, so DNS rebinding between validation and connect cannot smuggle
// in a private address.
func clipURLDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses for host %s", host)
	}
	for _, ip := range ips {
		if !clipURLIPAllowed(ip, addr) {
			return nil, fmt.Errorf("%w: host resolves to a disallowed address", errClipURLBlocked)
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func clipURLAddr(host string, u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port)
}

func clipURLIPAllowed(ip net.IP, addr string) bool {
	if !isDisallowedClipIP(ip) {
		return true
	}
	return clipURLAllowPrivateHook != nil && clipURLAllowPrivateHook(addr)
}

func isDisallowedClipIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

// extractHTMLTitle pulls the <title> out of a page, scanning only the head of
// the document and collapsing whitespace.
func extractHTMLTitle(data []byte) string {
	head := data
	if len(head) > 1<<20 {
		head = head[:1<<20]
	}
	match := clipURLTitleRE.FindSubmatch(head)
	if match == nil {
		return ""
	}
	return strings.Join(strings.Fields(html.UnescapeString(string(match[1]))), " ")
}
