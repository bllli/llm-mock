package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"llm-mock/internal"

	"github.com/goccy/go-yaml"
	"go.uber.org/zap"

	_ "net/http/pprof"
)

var appConfig *internal.Config

func InitPprof() {
	runtime.SetMutexProfileFraction(10)
	runtime.SetBlockProfileRate(10)

	go func() {
		// 添加自定义的调度器统计
		http.HandleFunc("/debug/schedstats", func(w http.ResponseWriter, r *http.Request) {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)

			fmt.Fprintf(w, "NumGoroutine: %d\n", runtime.NumGoroutine())
			fmt.Fprintf(w, "GOMAXPROCS: %d\n", runtime.GOMAXPROCS(0))
			fmt.Fprintf(w, "NumCPU: %d\n", runtime.NumCPU())
			fmt.Fprintf(w, "NumGC: %d\n", stats.NumGC)

			// 添加这两行
			fmt.Fprintf(w, "Handler Entered: %d\n", internal.HandlerEnterCount.Load())
			fmt.Fprintf(w, "Handler Active: %d\n", internal.HandlerActiveCount.Load())

			// 获取所有 goroutine 的状态
			buf := make([]byte, 1024*1024)
			n := runtime.Stack(buf, true)

			// 统计各种状态
			content := string(buf[:n])
			runnable := strings.Count(content, "runnable")
			waiting := strings.Count(content, "chan receive")
			sleeping := strings.Count(content, "sleep")

			fmt.Fprintf(w, "Runnable: %d\n", runnable)
			fmt.Fprintf(w, "Chan Receive: %d\n", waiting)
			fmt.Fprintf(w, "Sleep: %d\n", sleeping)
		})

		// 暴露 pprof 接口
		pprofAddr := fmt.Sprintf("0.0.0.0:%d", appConfig.Server.PprofPort)
		internal.Logger.Info("pprof listening on " + pprofAddr)
		http.ListenAndServe(pprofAddr, nil)
	}()
}

func LoadConfig(configPath string) {
	internal.InitConfig(configPath)
	appConfig = internal.AppConfig
	yamlBytes, err := yaml.Marshal(appConfig)
	if err != nil {
		panic(err)
	}
	internal.Logger.Info("Config initialized: \n" + string(yamlBytes))
}

func main() {
	internal.InitLogger(zap.InfoLevel)

	configPath := flag.String("c", "config.yaml", "config file")
	flag.Parse()
	LoadConfig(*configPath)

	if appConfig.Server.PprofEnabled {
		InitPprof()
	}

	internal.InitTokens()
	internal.InitMetrics()

	engine := internal.NewServer()

	addr := fmt.Sprintf("0.0.0.0:%d", appConfig.Server.Port)

	readtimeout := time.Duration(appConfig.Server.ReadTimeout) * time.Second
	writetimeout := time.Duration(appConfig.Server.WriteTimeout) * time.Second

	server := &http.Server{
		Addr:         addr,
		Handler:      engine,
		ReadTimeout:  readtimeout,
		WriteTimeout: writetimeout,
	}

	internal.Logger.Info("Server initialized: " + addr + ", read timeout: " + readtimeout.String() + ", write timeout: " + writetimeout.String())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		internal.Logger.Info("Starting server: " + addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			internal.Logger.Error("listen error", zap.Error(err))
		}
	}()

	<-ctx.Done()

	stop()
	internal.Logger.Info("shutting down gracefully, press Ctrl+C again to force")

	// Mark server as shutting down to stop accepting new requests
	internal.SetShuttingDown()
	internal.Logger.Info("Server marked as shutting down, no longer accepting new requests")

	// Close the listener to stop accepting new connections
	if err := server.Close(); err != nil {
		internal.Logger.Error("server close error", zap.Error(err))
	}

	// Wait for all active requests to complete (up to 30 seconds)
	internal.Logger.Info("Waiting for active requests to complete...")
	internal.WaitForAllRequests(30 * time.Second)

	internal.Logger.Info("server exiting")
}
