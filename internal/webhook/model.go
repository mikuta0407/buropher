// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package webhook

import (
	"context"
	"strings"
	"unicode/utf8"
)

// 長さの上限（validates :url, length: { maximum: 2000 } / :secret, length: { maximum: 255 }）。
const (
	MaxURLLength    = 2000
	MaxSecretLength = 255
)

// FieldError は 1 件の検証エラー（errors.add(attr, key, opts)）。
type FieldError struct {
	Attr string
	Key  string
	Opts map[string]any
}

// Validate は Webhook の validates（url の必須・送信先・長さ、secret の長さ、events の配列）。
// events は check_events_array と同じく空文字を除いた結果を返す。
// エラーの順序は Rails の宣言順（url の presence → webhook_endpoint → length、secret、events）。
func Validate(ctx context.Context, v *Validator, url, secret string, events []string) ([]FieldError, []string) {
	var errs []FieldError
	if strings.TrimSpace(url) == "" {
		errs = append(errs, FieldError{Attr: "url", Key: "blank"})
	} else {
		if v == nil {
			v = NewValidator(nil)
		}
		if !v.SafeURL(ctx, url) {
			errs = append(errs, FieldError{Attr: "url", Key: "invalid"})
		}
	}
	if utf8.RuneCountInString(url) > MaxURLLength {
		errs = append(errs, FieldError{Attr: "url", Key: "too_long", Opts: map[string]any{"count": MaxURLLength}})
	}
	if secret != "" && utf8.RuneCountInString(secret) > MaxSecretLength {
		errs = append(errs, FieldError{Attr: "secret", Key: "too_long", Opts: map[string]any{"count": MaxSecretLength}})
	}
	cleaned := make([]string, 0, len(events))
	invalid := false
	for _, e := range events {
		if strings.TrimSpace(e) == "" {
			continue
		}
		cleaned = append(cleaned, e)
		if !ValidEvent(e) {
			invalid = true
		}
	}
	if invalid {
		errs = append(errs, FieldError{Attr: "events", Key: "invalid"})
	}
	return errs, cleaned
}
