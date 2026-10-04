// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Record はセッションの永続化単位（DB の sessions テーブル 1 行に相当）。
// Data は JSON にシリアライズ可能な値のみを入れること（DB 実装では JSON カラムに保存する想定）。
type Record struct {
	ID        string
	UserID    int64 // 0 = 未ログイン（Redmine の session[:user_id]）
	Data      map[string]any
	CreatedAt time.Time // ログイン（セッション開始）時刻。session_lifetime の基準
	UpdatedAt time.Time // 最終アクセス時刻（1 分単位で更新）。session_timeout の基準
	ExpiresAt time.Time // 掃除用の失効時刻（ゼロ値 = 無期限）
	SudoAt    time.Time // Redmine の session[:sudo_timestamp]（ゼロ値 = なし）
	IP        string
	UserAgent string
}

// Store はサーバサイドセッションの保存先。
// 実装はゴルーチン安全であること。Get は存在しなければ (nil, nil) を返す。
type Store interface {
	Get(ctx context.Context, id string) (*Record, error)
	// Save は ID をキーに upsert する（新しい ID のセッションの作成に使う）。
	Save(ctx context.Context, rec *Record) error
	// Update は既存のセッションだけを更新する。レコードが無ければ（DestroyAllForUser・Destroy 等で
	// 削除済みなら）作り直さず false を返す。読み込み後に並行して失効させられたセッションが、
	// 処理中のリクエストのコミットで復活しないようにするため。
	Update(ctx context.Context, rec *Record) (bool, error)
	Destroy(ctx context.Context, id string) error
	// DestroyAllForUser はユーザの全セッションを削除する（exceptID は残す。空なら全削除）。
	// パスワード変更・ロック時に使う（Redmine の tokens.action='session' 全削除相当）。
	DestroyAllForUser(ctx context.Context, userID int64, exceptID string) error
}

// ExpiryPolicy は Redmine の session_lifetime / session_timeout 設定（0 = 無効）。
type ExpiryPolicy struct {
	Lifetime time.Duration // セッション開始からの最大時間
	Timeout  time.Duration // 最終アクセスからの最大無操作時間
}

// PolicyFromMinutes は Setting.session_lifetime / session_timeout（分）から ExpiryPolicy を作る。
func PolicyFromMinutes(lifetime, timeout int) ExpiryPolicy {
	return ExpiryPolicy{Lifetime: time.Duration(lifetime) * time.Minute, Timeout: time.Duration(timeout) * time.Minute}
}

// Valid は User.verify_session_token の判定部分を移植したもの:
// lifetime: created > now - lifetime、timeout: updated > now - timeout。
func (p ExpiryPolicy) Valid(rec *Record, now time.Time) bool {
	if p.Lifetime > 0 && !rec.CreatedAt.After(now.Add(-p.Lifetime)) {
		return false
	}
	if p.Timeout > 0 && !rec.UpdatedAt.After(now.Add(-p.Timeout)) {
		return false
	}
	return true
}

// expiresAt は掃除用の失効時刻を計算する。
func (p ExpiryPolicy) expiresAt(rec *Record) time.Time {
	var t time.Time
	if p.Lifetime > 0 {
		t = rec.CreatedAt.Add(p.Lifetime)
	}
	if p.Timeout > 0 {
		u := rec.UpdatedAt.Add(p.Timeout)
		if t.IsZero() || u.Before(t) {
			t = u
		}
	}
	return t
}

// touchInterval は updated_on を更新する最小間隔（verify_session_token の 1.minute）。
const touchInterval = time.Minute

// maxCookieBytes はクライアント側に保存する場合のクッキー値の上限（Rails の CookieOverflow は 4096）。
const maxCookieBytes = 4000

// SessionManager はセッションの読み込み・保存とクッキーを管理する。
type SessionManager struct {
	// Store はサーバサイドの保存先（必須）。
	Store Store
	// Secret はクッキー署名・暗号化の鍵の元（config の secret_key）。
	Secret []byte
	// CookieName は既定 "_redmine_session"。
	CookieName string
	// CookiePath は既定 "/"（relative_url_root があればそれ）。
	CookiePath string
	// Secure はクッキーに Secure を付けるか（nil なら HTTPS リクエスト時のみ）。
	Secure func(r *http.Request) bool
	// Policy はログイン済みセッションの失効設定を返す（nil なら無期限）。
	// ExpiresAt の計算に使う。失効判定自体は Session.Expired で行う。
	Policy func() ExpiryPolicy
	// AnonymousTTL は未ログインセッションの無操作保持期間（既定 7 日）。
	AnonymousTTL time.Duration
	// ServerSideAnonymous が false（既定）なら、未ログインかつ小さいセッションは
	// 暗号化クッキーに保存し Store を使わない（クッキーを保持しないボットで行が増えるのを防ぐ）。
	// ログイン済み・4KB 超・sudo ありのセッションは常に Store に保存する。
	ServerSideAnonymous bool
	// Now は現在時刻（テスト用。nil なら time.Now）。
	Now func() time.Time
	// Logger はコミット時のエラー出力先（nil なら slog.Default()）。
	Logger *slog.Logger

	keysOnce sync.Once
	signKey  []byte
	aead     cipher.AEAD
}

func (m *SessionManager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *SessionManager) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

func (m *SessionManager) cookieName() string {
	if m.CookieName != "" {
		return m.CookieName
	}
	return "_redmine_session"
}

func (m *SessionManager) cookiePath() string {
	if m.CookiePath != "" {
		return m.CookiePath
	}
	return "/"
}

func (m *SessionManager) anonymousTTL() time.Duration {
	if m.AnonymousTTL > 0 {
		return m.AnonymousTTL
	}
	return 7 * 24 * time.Hour
}

func (m *SessionManager) policy() ExpiryPolicy {
	if m.Policy != nil {
		return m.Policy()
	}
	return ExpiryPolicy{}
}

// DeriveKey は secret から用途別の 32 バイト鍵を導出する（HMAC-SHA256(secret, purpose)）。
func DeriveKey(secret []byte, purpose string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(purpose))
	return h.Sum(nil)
}

func (m *SessionManager) keys() {
	m.keysOnce.Do(func() {
		if len(m.Secret) == 0 {
			panic("httpx: SessionManager.Secret is empty")
		}
		m.signKey = DeriveKey(m.Secret, "buropher.session.sign")
		block, err := aes.NewCipher(DeriveKey(m.Secret, "buropher.session.encrypt"))
		if err != nil {
			panic(err)
		}
		m.aead, err = cipher.NewGCM(block)
		if err != nil {
			panic(err)
		}
	})
}

// newSessionID は 32 バイトの乱数 ID（base64url）を返す。
func newSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (m *SessionManager) sign(payload string) string {
	m.keys()
	h := hmac.New(sha256.New, m.signKey)
	h.Write([]byte("session:" + payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// serverCookieValue は "s.<id>.<kind>.<hmac>"。kind はログイン済みなら "u"、未ログインなら "a"。
func (m *SessionManager) serverCookieValue(id string, loggedIn bool) string {
	kind := "a"
	if loggedIn {
		kind = "u"
	}
	return "s." + id + "." + kind + "." + m.sign(id+"."+kind)
}

// parseServerCookie は署名を検証して ID とログイン済みフラグを返す。
func (m *SessionManager) parseServerCookie(v string) (id string, loggedIn bool, ok bool) {
	rest, found := strings.CutPrefix(v, "s.")
	if !found {
		return "", false, false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 || parts[0] == "" || (parts[1] != "u" && parts[1] != "a") {
		return "", false, false
	}
	want := m.sign(parts[0] + "." + parts[1])
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(want)) != 1 {
		return "", false, false
	}
	return parts[0], parts[1] == "u", true
}

type clientPayload struct {
	Data    map[string]any `json:"d"`
	Created int64          `json:"c"`
	Updated int64          `json:"u"`
}

// clientCookieValue は未ログインセッションを "c.<base64url(nonce|AES-GCM)>" に暗号化する。
func (m *SessionManager) clientCookieValue(rec *Record) (string, error) {
	m.keys()
	pt, err := json.Marshal(clientPayload{Data: rec.Data, Created: rec.CreatedAt.Unix(), Updated: rec.UpdatedAt.Unix()})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := m.aead.Seal(nonce, nonce, pt, []byte(m.cookieName()))
	return "c." + base64.RawURLEncoding.EncodeToString(ct), nil
}

func (m *SessionManager) parseClientCookie(v string) (*Record, bool) {
	rest, ok := strings.CutPrefix(v, "c.")
	if !ok {
		return nil, false
	}
	m.keys()
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil || len(raw) < m.aead.NonceSize() {
		return nil, false
	}
	pt, err := m.aead.Open(nil, raw[:m.aead.NonceSize()], raw[m.aead.NonceSize():], []byte(m.cookieName()))
	if err != nil {
		return nil, false
	}
	var p clientPayload
	if err := json.Unmarshal(pt, &p); err != nil {
		return nil, false
	}
	if p.Data == nil {
		p.Data = map[string]any{}
	}
	return &Record{Data: p.Data, CreatedAt: time.Unix(p.Created, 0), UpdatedAt: time.Unix(p.Updated, 0)}, true
}

// Session はリクエスト中のセッション（Rails の session 相当）。ゴルーチン安全ではない。
type Session struct {
	m          *SessionManager
	rec        Record
	persisted  bool   // Store に存在する
	fromClient bool   // 暗号化クッキーから読み込んだ
	cookieVal  string // 受信したクッキー値
	dirty      bool
	oldIDs     []string
	flash      *Flash
	committed  bool
	detached   bool // DetachSession で作った使い捨てのセッション（保存しない）
	r          *http.Request
}

// SessionOf はリクエストのセッションを返す（Middleware 未実行なら nil）。
func SessionOf(r *http.Request) *Session {
	s, _ := r.Context().Value(ctxSession).(*Session)
	return s
}

// Load はリクエストのクッキーからセッションを読み込む（Middleware が内部で使う。テスト用に公開）。
func (m *SessionManager) Load(r *http.Request) *Session {
	s := &Session{m: m, r: r}
	now := m.now()
	if c, err := r.Cookie(m.cookieName()); err == nil && c.Value != "" {
		s.cookieVal = c.Value
		if id, _, ok := m.parseServerCookie(c.Value); ok {
			rec, err := m.Store.Get(r.Context(), id)
			if err != nil {
				m.logger().Error("session load failed", "err", err)
			} else if rec != nil && (rec.ExpiresAt.IsZero() || rec.ExpiresAt.After(now) || rec.UserID != 0) {
				// ログイン済みセッションは ExpiresAt を過ぎていても読み込み、
				// 失効判定（Expired）と Redmine 互換の処理（flash + ログイン画面）は上位に任せる。
				s.rec = *rec
				if s.rec.Data == nil {
					s.rec.Data = map[string]any{}
				}
				s.persisted = true
			}
		} else if rec, ok := m.parseClientCookie(c.Value); ok {
			if now.Sub(rec.UpdatedAt) <= m.anonymousTTL() {
				s.rec = *rec
				s.fromClient = true
			}
		}
	}
	if !s.persisted && !s.fromClient {
		s.rec = Record{Data: map[string]any{}, CreatedAt: now, UpdatedAt: now}
	}
	return s
}

// Middleware はセッションを読み込み、レスポンスヘッダ送出前に保存・クッキー発行を行う。
func (m *SessionManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := m.Load(r)
		ctx := context.WithValue(r.Context(), ctxSession, s)
		r = r.WithContext(ctx)
		s.r = r
		cw := &commitWriter{ResponseWriter: w}
		cw.commit = func() { s.Commit(w) }
		next.ServeHTTP(cw, r)
		cw.doCommit()
	})
}

// DetachSession は、リクエストのセッションを空の使い捨てのセッションに差し替えたリクエストを返す。
// クッキーから読み込んだ値（ログイン・flash・処理途中のトークン等）は見えず、書き込みは保存されない
// （Store にもクッキーにも反映しない）。元のセッションはそのまま残る。セッションが無ければ r を返す。
func DetachSession(r *http.Request) *http.Request {
	s := SessionOf(r)
	if s == nil {
		return r
	}
	now := s.m.now()
	d := &Session{m: s.m, rec: Record{Data: map[string]any{}, CreatedAt: now, UpdatedAt: now}, detached: true, committed: true}
	r = r.WithContext(context.WithValue(r.Context(), ctxSession, d))
	d.r = r
	return r
}

// ID はセッション ID を返す（未保存の新規セッションは空）。
func (s *Session) ID() string { return s.rec.ID }

// IsNew は既存セッションを読み込めなかった（新規）なら true。
func (s *Session) IsNew() bool { return !s.persisted && !s.fromClient }

// Record は現在のレコードのコピーを返す。
func (s *Session) Record() Record { return s.rec }

// Get は値を返す。
func (s *Session) Get(key string) any { return s.rec.Data[key] }

// Has はキーが存在するかを返す。
func (s *Session) Has(key string) bool { _, ok := s.rec.Data[key]; return ok }

// GetString は文字列値を返す（文字列以外は ValueString で変換）。
func (s *Session) GetString(key string) string { return ValueString(normalizeNumber(s.rec.Data[key])) }

// GetInt は整数値を返す（JSON 往復で float64 になった値も扱う）。
func (s *Session) GetInt(key string) int64 { return ValueInt(normalizeNumber(s.rec.Data[key])) }

func normalizeNumber(v any) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
	case int:
		return int64(x)
	case int32:
		return int64(x)
	}
	return v
}

// Set は値を設定する（JSON シリアライズ可能な値のみ）。
func (s *Session) Set(key string, v any) {
	s.rec.Data[key] = v
	s.dirty = true
}

// Delete はキーを削除する。
func (s *Session) Delete(key string) {
	if _, ok := s.rec.Data[key]; ok {
		delete(s.rec.Data, key)
		s.dirty = true
	}
}

// UserID はログインユーザ ID（0 = 未ログイン）を返す。
func (s *Session) UserID() int64 { return s.rec.UserID }

// SetUserID はログインユーザを設定する。ログイン時は先に Reset を呼ぶこと
// （Redmine の logged_user= は reset_session してから start_user_session する）。
// CreatedAt/UpdatedAt も現在時刻にする（新しい session トークンの created_on 相当）。
func (s *Session) SetUserID(id int64) {
	now := s.m.now()
	s.rec.UserID = id
	s.rec.CreatedAt = now
	s.rec.UpdatedAt = now
	s.dirty = true
	s.persistLogin()
}

// persistLogin は開始したログインセッションの行をその場で Store に保存する（Redmine の
// start_user_session が session トークンを直ちに作るのと同じ）。レスポンス送出時（Commit）まで
// 保存を遅らせると、認証（autologin・パスワード等）の後、処理中にパスワード変更・2 要素認証の有効化・
// ロックで全セッションが破棄されても、Commit の upsert でこのリクエストのセッションが新しく作られ、
// 失効後も使えるログインが残った。先に保存しておけば破棄で消え、Commit は既存の行の更新だけを行う
// （消えていれば Cookie も出さない）。
func (s *Session) persistLogin() {
	if s.rec.UserID == 0 || s.persisted || s.r == nil || s.detached {
		return
	}
	now := s.m.now()
	rec := s.rec
	if rec.ID == "" {
		rec.ID = newSessionID()
	}
	rec.ExpiresAt = s.m.policy().expiresAt(&rec)
	if rec.IP == "" {
		rec.IP = RemoteIP(s.r)
		rec.UserAgent = s.r.UserAgent()
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = now
	}
	if err := s.m.Store.Save(context.WithoutCancel(s.r.Context()), &rec); err != nil {
		// 保存できなければ従来どおり Commit で保存する
		s.m.logger().Error("session save failed", "err", err)
		return
	}
	s.rec.ID, s.rec.ExpiresAt, s.rec.IP, s.rec.UserAgent = rec.ID, rec.ExpiresAt, rec.IP, rec.UserAgent
	s.persisted = true
	s.fromClient = false
}

// SudoAt は sudo モードの最終確認時刻を返す。
func (s *Session) SudoAt() time.Time { return s.rec.SudoAt }

// SetSudoAt は sudo モードの時刻を設定する（ゼロ値で解除）。
func (s *Session) SetSudoAt(t time.Time) {
	s.rec.SudoAt = t
	s.dirty = true
}

// CreatedAt はセッション開始時刻。
func (s *Session) CreatedAt() time.Time { return s.rec.CreatedAt }

// UpdatedAt は最終アクセス時刻（1 分単位）。
func (s *Session) UpdatedAt() time.Time { return s.rec.UpdatedAt }

// Expired は Redmine の session_expired? 相当。ログイン済みセッションのみ判定し、
// Policy に照らして無効なら true。Store から消された（パスワード変更等）セッションは
// そもそも読み込まれず UserID=0 になるため、併せて Revoked も確認すること。
func (s *Session) Expired(p ExpiryPolicy, now time.Time) bool {
	if s.rec.UserID == 0 {
		return false
	}
	return !p.Valid(&s.rec, now)
}

// Revoked は、ログイン済みを示す署名付きクッキーを受信したがレコードが存在しない
// （DestroyAllForUser・掃除ジョブ等で失効させられた）場合に true を返す。
// Redmine では session[:user_id] が残ったまま tk が無効になるケースで、
// error_session_expired を表示してログイン画面に送る挙動に対応する。
func (s *Session) Revoked() bool {
	if s.persisted || s.cookieVal == "" {
		return false
	}
	_, loggedIn, ok := s.m.parseServerCookie(s.cookieVal)
	return ok && loggedIn
}

// Reset は Rails の reset_session 相当: 旧セッションを破棄し、新しい ID・空データにする。
// flash と CSRF トークンも消える。ログイン・ログアウト・CSRF 失敗時に使う。
func (s *Session) Reset() {
	if s.persisted && s.rec.ID != "" {
		s.oldIDs = append(s.oldIDs, s.rec.ID)
	}
	now := s.m.now()
	s.rec = Record{Data: map[string]any{}, CreatedAt: now, UpdatedAt: now}
	s.persisted = false
	s.fromClient = false
	s.flash = nil
	s.dirty = true
}

// Renew はデータを保持したまま ID だけを新しくする（セッション固定化対策）。
func (s *Session) Renew() {
	if s.persisted && s.rec.ID != "" {
		s.oldIDs = append(s.oldIDs, s.rec.ID)
	}
	s.rec.ID = ""
	s.persisted = false
	s.dirty = true
}

func (s *Session) empty() bool {
	return len(s.rec.Data) == 0 && s.rec.UserID == 0 && s.rec.SudoAt.IsZero()
}

func (s *Session) secure() bool {
	if s.m.Secure != nil {
		return s.m.Secure(s.r)
	}
	return RequestScheme(s.r) == "https"
}

func (s *Session) setCookie(w http.ResponseWriter, value string, del bool) {
	c := &http.Cookie{
		Name:     s.m.cookieName(),
		Value:    value,
		Path:     s.m.cookiePath(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secure(),
	}
	if del {
		c.Value = ""
		c.MaxAge = -1
		c.Expires = time.Unix(0, 0)
	}
	http.SetCookie(w, c)
}

// Commit は flash を書き戻し、必要なら Store に保存して Set-Cookie を出す。
// Middleware がレスポンスヘッダ送出直前に自動で呼ぶ。2 回目以降は何もしない。
func (s *Session) Commit(w http.ResponseWriter) {
	if s.committed {
		return
	}
	s.committed = true
	ctx := context.WithoutCancel(s.r.Context())
	now := s.m.now()
	s.commitFlash()

	for _, id := range s.oldIDs {
		if err := s.m.Store.Destroy(ctx, id); err != nil {
			s.m.logger().Error("session destroy failed", "err", err)
		}
	}

	if s.empty() {
		if s.dirty || len(s.oldIDs) > 0 {
			if s.persisted && s.rec.ID != "" {
				if err := s.m.Store.Destroy(ctx, s.rec.ID); err != nil {
					s.m.logger().Error("session destroy failed", "err", err)
				}
			}
			if s.cookieVal != "" {
				s.setCookie(w, "", true)
			}
		}
		return
	}

	// 未ログインの小さいセッションは暗号化クッキーへ
	if !s.m.ServerSideAnonymous && s.rec.UserID == 0 && s.rec.SudoAt.IsZero() && !s.persisted {
		stale := now.Sub(s.rec.UpdatedAt) >= time.Hour
		if s.dirty || stale || !s.fromClient {
			if stale || s.dirty {
				s.rec.UpdatedAt = now
			}
			v, err := s.m.clientCookieValue(&s.rec)
			if err == nil && len(v) <= maxCookieBytes {
				s.setCookie(w, v, false)
				s.fromClient = true
				return
			}
			if err != nil {
				s.m.logger().Error("session cookie encode failed", "err", err)
			}
			// 大きすぎる場合はサーバ側へ
		} else {
			return
		}
	}

	needSave := s.dirty || !s.persisted || now.Sub(s.rec.UpdatedAt) >= touchInterval
	if !needSave {
		return
	}
	newID := false
	if s.rec.ID == "" {
		s.rec.ID = newSessionID()
		newID = true
	}
	if s.rec.CreatedAt.IsZero() {
		s.rec.CreatedAt = now
	}
	s.rec.UpdatedAt = now
	if s.rec.UserID != 0 {
		s.rec.ExpiresAt = s.m.policy().expiresAt(&s.rec)
	} else {
		s.rec.ExpiresAt = now.Add(s.m.anonymousTTL())
	}
	if s.rec.IP == "" {
		s.rec.IP = RemoteIP(s.r)
		s.rec.UserAgent = s.r.UserAgent()
	}
	rec := s.rec
	if s.persisted && !newID {
		// 読み込んだ既存セッションは更新のみ。処理中にパスワード変更・ロック等で削除されていたら
		// upsert で復活させない（次のリクエストで Revoked として扱われる）
		ok, err := s.m.Store.Update(ctx, &rec)
		if err != nil {
			s.m.logger().Error("session save failed", "err", err)
			return
		}
		if !ok {
			return
		}
	} else if err := s.m.Store.Save(ctx, &rec); err != nil {
		s.m.logger().Error("session save failed", "err", err)
		return
	}
	s.persisted = true
	cv := s.m.serverCookieValue(s.rec.ID, s.rec.UserID != 0)
	if newID || cv != s.cookieVal {
		s.setCookie(w, cv, false)
	}
}

// commitWriter はヘッダ送出前に一度だけ commit を呼ぶ ResponseWriter。
type commitWriter struct {
	http.ResponseWriter
	once   sync.Once
	commit func()
}

func (c *commitWriter) doCommit() { c.once.Do(c.commit) }

func (c *commitWriter) WriteHeader(code int) {
	c.doCommit()
	c.ResponseWriter.WriteHeader(code)
}

func (c *commitWriter) Write(b []byte) (int, error) {
	c.doCommit()
	return c.ResponseWriter.Write(b)
}

func (c *commitWriter) Flush() {
	c.doCommit()
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap は http.ResponseController 用。
func (c *commitWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// ---- メモリ実装 ----

// MemoryStore はプロセス内メモリの Store 実装（開発・テスト・単一プロセス用）。
// Data は保存時に JSON で往復させ、DB 実装と同じ型（数値は float64 等）になるようにする。
type MemoryStore struct {
	mu   sync.Mutex
	recs map[string]*Record
}

// NewMemoryStore は空の MemoryStore を返す。
func NewMemoryStore() *MemoryStore { return &MemoryStore{recs: map[string]*Record{}} }

func copyRecord(rec *Record) (*Record, error) {
	cp := *rec
	b, err := json.Marshal(rec.Data)
	if err != nil {
		return nil, err
	}
	cp.Data = map[string]any{}
	if err := json.Unmarshal(b, &cp.Data); err != nil {
		return nil, err
	}
	if cp.Data == nil {
		cp.Data = map[string]any{}
	}
	return &cp, nil
}

// Get は Store.Get の実装。
func (s *MemoryStore) Get(_ context.Context, id string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recs[id]
	if !ok {
		return nil, nil
	}
	return copyRecord(rec)
}

// Save は Store.Save の実装。
func (s *MemoryStore) Save(_ context.Context, rec *Record) error {
	if rec.ID == "" {
		return errors.New("httpx: session id is empty")
	}
	cp, err := copyRecord(rec)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs[rec.ID] = cp
	return nil
}

// Update は Store.Update の実装。
func (s *MemoryStore) Update(_ context.Context, rec *Record) (bool, error) {
	cp, err := copyRecord(rec)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.recs[rec.ID]; !ok {
		return false, nil
	}
	s.recs[rec.ID] = cp
	return true, nil
}

// Destroy は Store.Destroy の実装。
func (s *MemoryStore) Destroy(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recs, id)
	return nil
}

// DestroyAllForUser は Store.DestroyAllForUser の実装。
func (s *MemoryStore) DestroyAllForUser(_ context.Context, userID int64, exceptID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rec := range s.recs {
		if rec.UserID == userID && id != exceptID {
			delete(s.recs, id)
		}
	}
	return nil
}

// DeleteExpired は ExpiresAt を過ぎたレコードを削除し、件数を返す（定期掃除ジョブ用）。
func (s *MemoryStore) DeleteExpired(_ context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, rec := range s.recs {
		if !rec.ExpiresAt.IsZero() && !rec.ExpiresAt.After(now) {
			delete(s.recs, id)
			n++
		}
	}
	return n, nil
}

// Len は保持件数を返す（テスト用）。
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recs)
}
