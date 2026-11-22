package pricetable

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const publishedPrices = "https://raw.githubusercontent.com/Nathandelenclos/agent-stats/pricing-data/prices.json"

func cachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-stats", "prices.json"), nil
}

// Pull refreshes the cached price table from the published snapshot.
func Pull() (int, error) {
	path, err := cachePath()
	if err != nil {
		return 0, err
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return pull(client, publishedPrices, path)
}

func pull(client *http.Client, source, path string) (int, error) {
	resp, err := client.Get(source)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("price download: %s", resp.Status)
	}
	raw, err := readPrices(resp.Body)
	if err != nil {
		return 0, err
	}
	prices, err := decode(raw)
	if err != nil {
		return 0, fmt.Errorf("downloaded prices: %w", err)
	}
	if len(prices) == 0 {
		return 0, fmt.Errorf("downloaded price table is empty")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	temp, err := os.CreateTemp(dir, ".prices-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(raw)
	closeErr := temp.Close()
	if writeErr != nil {
		return 0, writeErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return 0, err
	}
	return len(prices), nil
}
