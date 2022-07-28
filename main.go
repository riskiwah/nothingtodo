package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/host"
)

type Stats struct {
	Hostname      string `json:"hostname"`
	Uptime        string `json:"uptime"`
	Platform      string `json:"platform"`
	KernelVersion string `json:"kernelVersion"`
	KernelArch    string `json:"kernelArch"`
}

// todo! add -ldflags (build time)
// references: https://blog.alexellis.io/inject-build-time-vars-golang/
var (
	//go:embed all:static
	staticFiles embed.FS

	buildTime string
	commit    string
)

// type RenderTemplate struct {
// 	templ *template.Template
// }

// todo! add dynamic path after /pprof/{goroutine,heap,allocs,etcetc}
// with switch case maybe (?)
type profiles string

// func formatDate(timeStamp time.Time) string {
// 	return timeStamp.Format("01-02-2006")
// }

// func render() (*RenderTemplate, error) {
// 	templ, err := template.ParseFS(staticFiles, "templates/*.gohtml")
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &RenderTemplate{templ: templ}, nil
// }

func handleStatic() http.Handler {
	rootFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Printf("Error: Failed to load FS: %v", err)
	}
	return http.FileServer(http.FS(rootFS))
}

func handlePprof(w http.ResponseWriter, req *http.Request) {
	getPprof := pprof.Lookup("heap")

	err := getPprof.WriteTo(w, 1)
	if err != nil {
		log.Printf("Error: Failed to write pprof: %v", err)
	}
}

func handleStatus(w http.ResponseWriter, req *http.Request) {
	stats, err := host.Info()
	if err != nil {
		log.Printf("Error: Failed to get status: %v", err)
	}

	convertTime := time.Duration(stats.Uptime) * time.Second

	statsData := Stats{
		Hostname:      stats.Hostname,
		Uptime:        convertTime.String(),
		Platform:      stats.Platform,
		KernelVersion: stats.KernelVersion,
		KernelArch:    stats.KernelArch,
	}

	marshalJson, err := json.Marshal(statsData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(marshalJson)
}

func gracefulShutdown(server *http.Server, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
		<-c
		log.Println("Server is shutting down...")

		ctx := context.Background()
		var cancel context.CancelFunc
		if timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		done <- server.Shutdown(ctx)
	}()

	log.Println("Starting HTTP server...")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	log.Println("Byeee")
	return <-done
}

func main() {
	router := http.NewServeMux()

	// handler
	router.Handle("/", handleStatic())
	router.HandleFunc("/pprof", handlePprof)
	router.HandleFunc("/status", handleStatus)

	server := &http.Server{
		Addr:        ":8080",
		Handler:     router,
		IdleTimeout: 60 * time.Second,
	}

	if err := gracefulShutdown(server, 10*time.Second); err != nil {
		log.Println(err)
	}
}
