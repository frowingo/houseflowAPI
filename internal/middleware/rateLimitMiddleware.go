package middleware

import (
	"fmt"
	"houseflowApi/internal/helpers"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

type RateLimitConfig struct {
	Max int

	Window time.Duration

	KeyFunc func(c *fiber.Ctx) string

	Message string

	Localizer helpers.MessageLocalizer
}

type windowEntry struct {
	count   int
	resetAt time.Time
}

func RateLimit(cfg RateLimitConfig) fiber.Handler {
	if cfg.KeyFunc == nil {
		cfg.KeyFunc = func(c *fiber.Ctx) string {
			return c.IP()
		}
	}
	if cfg.Message == "" {
		cfg.Message = "rate_limit.error.too_many_requests"
	}

	var storeMu sync.Mutex
	store := make(map[string]*windowEntry)

	go func() {
		ticker := time.NewTicker(cfg.Window * 2)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			storeMu.Lock()
			for key, entry := range store {
				if now.After(entry.resetAt) {
					delete(store, key)
				}
			}
			storeMu.Unlock()
		}
	}()

	return func(c *fiber.Ctx) error {
		key := cfg.KeyFunc(c)
		now := time.Now()

		storeMu.Lock()
		entry, ok := store[key]
		if !ok {
			entry = &windowEntry{resetAt: now.Add(cfg.Window)}
			store[key] = entry
		}

		if now.After(entry.resetAt) {
			entry.count = 0
			entry.resetAt = now.Add(cfg.Window)
		}

		entry.count++
		count := entry.count
		resetAt := entry.resetAt
		remaining := cfg.Max - entry.count
		if remaining < 0 {
			remaining = 0
		}

		c.Set("X-RateLimit-Limit", fmt.Sprintf("%d", cfg.Max))
		c.Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		storeMu.Unlock()

		c.Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetAt.Unix()))

		if count > cfg.Max {
			retryAfter := int(time.Until(resetAt).Seconds())
			if retryAfter < 0 {
				retryAfter = 0
			}
			c.Set("Retry-After", fmt.Sprintf("%d", retryAfter))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": helpers.LocalizedMessage(c, cfg.Localizer, cfg.Message),
			})
		}

		return c.Next()
	}
}

// IPRateLimit limits each IP to x requests per minute.
func IPRateLimit(localizers ...helpers.MessageLocalizer) fiber.Handler {
	return RateLimit(RateLimitConfig{
		Max:       50,
		Window:    time.Minute,
		Localizer: firstLocalizer(localizers),
	})
}

// StrictRateLimit limits each IP to x requests per y minutes.
func StrictRateLimit(localizers ...helpers.MessageLocalizer) fiber.Handler {
	return RateLimit(RateLimitConfig{
		Max:       10,
		Window:    15 * time.Minute,
		Message:   "rate_limit.error.too_many_attempts",
		Localizer: firstLocalizer(localizers),
	})
}

// UserRateLimit limits each authenticated user to x requests per minute.
// Falls back to IP when the user ID is not present in context.
// Must be placed after AuthRequired() in the middleware chain.
func UserRateLimit(localizers ...helpers.MessageLocalizer) fiber.Handler {
	return RateLimit(RateLimitConfig{
		Max:       20,
		Window:    time.Minute,
		Localizer: firstLocalizer(localizers),
		KeyFunc: func(c *fiber.Ctx) string {
			if userID, ok := c.Locals("userID").(string); ok && userID != "" {
				return "user:" + userID
			}
			return c.IP()
		},
	})
}

func firstLocalizer(localizers []helpers.MessageLocalizer) helpers.MessageLocalizer {
	if len(localizers) == 0 {
		return nil
	}
	return localizers[0]
}
