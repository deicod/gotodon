package services

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// InProcessLinkCrawler runs link crawling without a queue.
type InProcessLinkCrawler struct {
	Crawler *LinkCrawler
	Timeout time.Duration
	Logger  *slog.Logger
}

// Enqueue starts crawling in a background goroutine.
func (c *InProcessLinkCrawler) Enqueue(_ context.Context, statusID int64, url string) error {
	if c == nil || c.Crawler == nil {
		return ErrLinkCrawlerSkipRetry
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultLinkCrawlerTimeout
	}
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := c.Crawler.Crawl(ctx, statusID, url); err != nil && !errors.Is(err, ErrLinkCrawlerSkipRetry) {
			logger.Error("Link crawler failed", "status_id", statusID, "url", url, "error", err)
		}
	}()
	return nil
}
