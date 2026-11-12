package bootstrap

import (
	"context"
	"errors"
	"github.com/quic-go/webtransport-go"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/mediaobject"
	"sync"
	"time"
)

func (r *Runtime) Serve(parent context.Context) error {
	// 先停止接收并排空HTTP，再取消连接与任务，最后由调用方关闭资源池。
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	defer cancel()
	server := &http.Server{Addr: r.HTTP.Config.Addr, Handler: r.HTTP.Routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	var transport *webtransport.Server
	var err error
	if r.HTTP.Config.ChatWTAddr != "" {
		transport, err = r.HTTP.NewChatTransport()
		if err != nil {
			return err
		}
	}
	results := make(chan error, 2)
	if transport != nil {
		go func() { results <- transport.ListenAndServe() }()
	}
	go func() {
		slog.Info("API listening", "address", server.Addr, "surface", r.HTTP.Config.Surface)
		results <- server.ListenAndServe()
	}()
	var jobs sync.WaitGroup
	start := func(fn func()) { jobs.Add(1); go func() { defer jobs.Done(); fn() }() }
	// 双进程共用数据库，只有public进程运行周期任务，避免重复重算。
	if r.HTTP.Config.Surface == "public" {
		if r.Media != nil {
			start(func() {
				runJob(ctx, 5*time.Minute, time.Minute, true, func(job context.Context) error {
					_, err := mediaobject.CleanupSpool(filepath.Join(r.HTTP.Config.MediaDir, "r20-tmp"), time.Now())
					if err == nil {
						_, err = (&mediafiles.Store{Root: r.HTTP.Config.MediaDir}).CleanupScratch(time.Now())
					}
					return err
				})
			})
			start(func() {
				for ctx.Err() == nil {
					job, stop := context.WithTimeout(ctx, 10*time.Minute)
					done, err := r.Media.RunOnce(job)
					stop()
					if err != nil && ctx.Err() == nil {
						slog.Warn("media lifecycle task failed")
					}
					if !done || err != nil {
						select {
						case <-ctx.Done():
							return
						case <-time.After(2 * time.Second):
						}
					}
				}
			})
		}
		start(func() {
			runJob(ctx, 5*time.Minute, 45*time.Second, true, func(job context.Context) error {
				result, err := r.Maintenance.CleanupDrafts(job, 100)
				if result.Expired > 0 || result.Removed > 0 {
					slog.Info("draft cleanup completed", "expired", result.Expired, "removed_uploads", result.Removed)
				}
				return err
			})
		})
		start(func() { r.Recommendations.RunItemCF(ctx) })
		start(func() {
			runJob(ctx, 15*time.Second, 8*time.Second, false, func(job context.Context) error { return r.Chat.Dispatch(job) })
		})
		start(func() {
			runJob(ctx, 15*time.Minute, 45*time.Second, true, func(job context.Context) error { return r.Recommendations.RefreshPopular(job, time.Time{}) })
		})
	}
	select {
	case <-parent.Done():
	case err = <-results:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	shutdown, stop := context.WithTimeout(context.Background(), 15*time.Second)
	shutdownErr := server.Shutdown(shutdown)
	stop()
	if shutdownErr != nil {
		_ = server.Close()
	}
	cancel()
	if transport != nil {
		_ = transport.Close()
	}
	chatWait, chatStop := context.WithTimeout(context.Background(), 5*time.Second)
	chatErr := r.HTTP.WaitForChatSessions(chatWait)
	chatStop()
	jobs.Wait()
	if err != nil {
		return err
	}
	return errors.Join(shutdownErr, chatErr)
}

func runJob(ctx context.Context, period, budget time.Duration, immediate bool, run func(context.Context) error) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		if !immediate {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		immediate = false
		job, cancel := context.WithTimeout(ctx, budget)
		if err := run(job); err != nil && ctx.Err() == nil {
			slog.Warn("background job failed", "error", err)
		}
		cancel()
	}
}
