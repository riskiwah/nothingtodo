package main

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/host"
)

// todo! add -ldflags (build time)
// references: https://blog.alexellis.io/inject-build-time-vars-golang/
var (
	//go:embed all:static
	staticFiles    embed.FS
	commit         string
	buildTimestamp string
	year           string
)

type TemplateData struct {
	Footer Footer
	Stats  Stats
}

type Footer struct {
	// Timestamp     time.Time
	// FormattedTime string
	BuildTimestamp string
	CommitHash     string
	Year           string
}

type Stats struct {
	Hostname      string
	Uptime        string
	KernelVersion string
	KernelArch    string
}

// todo! add dynamic path after /pprof/{goroutine,heap,allocs,etcetc}
// with switch case maybe (?)
// type profiles string

func renderTemplate(stats Stats) (TemplateData, error) {
	stats, err := getStatus()
	if err != nil {
		return TemplateData{}, err
	}

	footer := Footer{
		BuildTimestamp: buildTimestamp,
		CommitHash:     commit,
		Year:           year,
	}

	templateData := TemplateData{
		Stats:  stats,
		Footer: footer,
	}

	return templateData, nil
}

func handleStatic(tpl *template.Template) http.Handler {
	rootFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Printf("Error: Failed to load FS: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			rendering, err := renderTemplate(Stats{})
			if err != nil {
				log.Printf("Error rendering template: %v", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			err = tpl.Execute(w, rendering)
			if err != nil {
				log.Printf("Error rendering template: %v", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
			return
		}
		http.FileServer(http.FS(rootFS)).ServeHTTP(w, r)
	})
}

func handlePprof(w http.ResponseWriter, req *http.Request) {
	getPprof := pprof.Lookup("heap")

	err := getPprof.WriteTo(w, 1)
	if err != nil {
		log.Printf("Error: Failed to write pprof: %v", err)
	}
}

func getStatus() (Stats, error) {
	stats, err := host.Info()
	if err != nil {
		return Stats{}, err
	}

	convertTime := time.Duration(stats.Uptime) * time.Second

	return Stats{
		Hostname:      stats.Hostname,
		Uptime:        convertTime.String(),
		KernelVersion: stats.KernelVersion,
		KernelArch:    stats.KernelArch,
	}, nil
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

// main function
func main() {
	router := http.NewServeMux()

	tpl, err := template.ParseFS(staticFiles, "static/index.html")
	if err != nil {
		log.Fatalf("Error parsing template: %s", err)
	}

	// handler
	router.Handle("/", handleStatic(tpl))
	router.HandleFunc("/pprof", handlePprof)

	server := &http.Server{
		Addr:        ":8080",
		Handler:     router,
		IdleTimeout: 60 * time.Second,
	}

	if err := gracefulShutdown(server, 10*time.Second); err != nil {
		log.Println(err)
	}
}
