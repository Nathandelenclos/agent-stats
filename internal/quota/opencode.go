package quota

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

const (
	openCodeUsagePath = "/api/experimental/usage/limits"
	maxOpenCodeUsage  = 1 << 20
)

type openCodeMeter struct {
	Used    *float64 `json:"percent"`
	ResetMS int64    `json:"resetsAt"`
}

// ReadOpenCodeGo asks an existing local OpenCode server for its Console account's Go allowance.
func ReadOpenCodeGo(ctx context.Context, baseURL string) []Window {
	endpoint, err := url.Parse(baseURL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawPath != "" ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return nil
	}
	addr, err := netip.ParseAddrPort(endpoint.Host)
	if err != nil || addr.Port() == 0 || addr.Addr().Zone() != "" || !addr.Addr().IsLoopback() {
		return nil
	}
	endpoint.Path = openCodeUsagePath
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil
	}
	request.Header.Set("Accept", "application/json")
	client := http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOpenCodeUsage+1))
	if err != nil || len(body) > maxOpenCodeUsage {
		return nil
	}
	var result struct {
		Data struct {
			Go struct {
				Rolling openCodeMeter `json:"rolling"`
				Weekly  openCodeMeter `json:"weekly"`
				Monthly openCodeMeter `json:"monthly"`
			} `json:"go"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &result) != nil {
		return nil
	}
	goAllowance := result.Data.Go
	now := time.Now()
	var windows []Window
	for _, entry := range []struct {
		label string
		meter openCodeMeter
	}{
		{"5-hour limit", goAllowance.Rolling},
		{"7-day limit", goAllowance.Weekly},
		{"Monthly limit", goAllowance.Monthly},
	} {
		if entry.meter.Used == nil || entry.meter.ResetMS <= 0 {
			continue
		}
		if window, ok := validWindow("OpenCode Go", entry.label, *entry.meter.Used, time.UnixMilli(entry.meter.ResetMS), now); ok {
			windows = append(windows, window)
		}
	}
	return windows
}
