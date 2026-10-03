package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// defaultPprofAddr は server.pprof_addr 未指定時の pprof の待ち受けアドレス。
const defaultPprofAddr = "127.0.0.1:6060"

// pprofHandler は net/http/pprof のハンドラ群 (/debug/pprof/...)。
// http.DefaultServeMux には登録しない (本体のルーターと混ざらないよう専用の mux を使う)。
func pprofHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	// SQL の実行統計（正規化したクエリ文ごとの回数・合計時間）。記録は既定で無効。
	// ?enable=1 / ?enable=0 で記録の開始 / 停止、?reset=1 で消去、?limit=N で上位 N 件を表示
	// (?full=1 でクエリ文を切り詰めない)。
	mux.HandleFunc("/debug/sqlstats", func(w http.ResponseWriter, r *http.Request) {
		if v := r.URL.Query().Get("enable"); v != "" {
			db.EnableQueryStats(v == "1")
			fmt.Fprintln(w, "enable =", v == "1")
			return
		}
		if r.URL.Query().Get("reset") == "1" {
			db.ResetQueryStats()
			fmt.Fprintln(w, "reset")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		db.WriteQueryStats(w, limit, r.URL.Query().Get("full") == "1")
	})
	return mux
}

// checkLoopbackAddr は addr がループバックアドレスに束縛されることを確かめる。
// pprof はプロセスの内部情報を返すため、外部から到達できるアドレスでは起動しない。
func checkLoopbackAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("server: pprof_addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("server: pprof_addr %q must be a loopback address (127.0.0.1 / ::1 / localhost)", addr)
	}
	return nil
}

// startPprof は server.pprof = true のとき pprof 用の HTTP サーバを別ポートで起動する。
// ctx の終了で停止する。
func (s *Server) startPprof(ctx context.Context) error {
	if !s.cfg.Server.Pprof {
		return nil
	}
	addr := s.cfg.Server.PprofAddr
	if addr == "" {
		addr = defaultPprofAddr
	}
	if err := checkLoopbackAddr(addr); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("server: pprof listen: %w", err)
	}
	srv := &http.Server{Handler: pprofHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		slog.Info("pprof listening", "addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("pprof server", "err", err)
		}
	}()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	return nil
}
