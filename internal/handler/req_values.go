package handler

import "context"

// setValue は before_action で取得したモデル（@user, @group など）をリクエストに持たせる。
// key は型付きの空構造体を使う（例 type userCtxKey struct{}）。
func (c *Req) setValue(key, v any) {
	c.R = c.R.WithContext(context.WithValue(c.R.Context(), key, v))
}

// value は setValue で持たせた値（無ければ nil）。
func (c *Req) value(key any) any { return c.R.Context().Value(key) }
