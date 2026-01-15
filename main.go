package main

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/host"
)

var (
	//go:embed all:static
	staticFiles  embed.FS
	weatherCache struct {
		Location    string
		Temperature int
		Condition   string
		Humidity    int
		Updated     time.Time
	}
	weatherMu      sync.RWMutex
	commit         string
	buildTimestamp string
	year           string
)

var httpClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

type Footer struct {
	BuildTimestamp string
	CommitHash     string
	Year           string
}

type wttrResponse struct {
	NearestArea []struct {
		AreaName []struct {
			Value string `json:"value"`
		} `json:"areaName"`
	} `json:"nearest_area"`
	CurrentCondition []struct {
		TempC       string `json:"temp_C"`
		Humidity    string `json:"humidity"`
		WeatherDesc []struct {
			Value string `json:"value"`
		} `json:"weatherDesc"`
	} `json:"current_condition"`
}

type KernelInfo struct {
	Arch    string `json:"arch"`
	Version string `json:"version"`
}

type HostEntry struct {
	Hostname string     `json:"hostname"`
	Kernel   KernelInfo `json:"kernel"`
	Uptime   string     `json:"uptime"`
}

type StatusJSON struct {
	Host    []HostEntry `json:"host"`
	Weather struct {
		Location    string `json:"location"`
		Temperature int    `json:"temperature"`
		Condition   string `json:"condition"`
		Humidity    int    `json:"humidity"`
	} `json:"weather"`
}

const (
	weatherLocation       = "Sleman"
	weatherCacheFile      = "data/weather_cache.json"
	weatherUpdateInterval = 30 * time.Minute
	maxRetries            = 3
)

func formatUptime(seconds uint64) string {
	if seconds == 0 {
		return "just booted"
	}
	d := time.Duration(seconds) * time.Second

	days := int(math.Floor(d.Hours() / 24))
	hours := int(math.Floor(d.Hours())) % 24
	mins := int(math.Floor(d.Minutes())) % 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d day%s", days, plural(days)))
	}
	if hours > 0 || days > 0 {
		parts = append(parts, fmt.Sprintf("%d hour%s", hours, plural(hours)))
	}
	if mins > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d minute%s", mins, plural(mins)))
	}

	return strings.Join(parts, ", ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// fetchWeatherFromAPI – isolated network logic with exactly 3 retries
func fetchWeatherFromAPI() (string, int, string, int, error) {
	url := "https://wttr.in/" + weatherLocation + "?format=j1"

	var resp *http.Response
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("User-Agent", "curl/8.4.0")

		resp, err = httpClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}

		if resp != nil {
			resp.Body.Close()
		}

		if attempt < maxRetries {
			sleep := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			log.Printf("Weather fetch attempt %d/%d failed: %v – retrying in %s", attempt, maxRetries, err, sleep)
			time.Sleep(sleep)
		}
	}

	if err != nil {
		return "", 0, "", 0, fmt.Errorf("failed after %d retries: %w", maxRetries, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return "", 0, "", 0, fmt.Errorf("HTTP %d after retries", resp.StatusCode)
	}

	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var data wttrResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return "", 0, "", 0, err
	}

	if len(data.CurrentCondition) == 0 {
		return "", 0, "", 0, fmt.Errorf("no current condition")
	}
	cc := data.CurrentCondition[0]

	location := weatherLocation
	if len(data.NearestArea) > 0 && len(data.NearestArea[0].AreaName) > 0 {
		location = data.NearestArea[0].AreaName[0].Value
	}

	condition := "Unknown"
	if len(cc.WeatherDesc) > 0 {
		condition = cc.WeatherDesc[0].Value
	}

	temp, _ := strconv.Atoi(cc.TempC)
	hum, _ := strconv.Atoi(cc.Humidity)

	return location, temp, condition, hum, nil
}

// loadWeatherCache loads cache from disk on startup
func loadWeatherCache() bool {
	data, err := os.ReadFile(weatherCacheFile)
	if err != nil {
		log.Printf("No weather cache file (normal on first run): %v", err)
		return false
	}

	var cached struct {
		Location    string    `json:"location"`
		Temperature int       `json:"temperature"`
		Condition   string    `json:"condition"`
		Humidity    int       `json:"humidity"`
		Updated     time.Time `json:"updated"`
	}

	if err := json.Unmarshal(data, &cached); err != nil {
		log.Printf("Corrupt weather cache file – ignoring: %v", err)
		return false
	}

	weatherMu.Lock()
	weatherCache.Location = cached.Location
	weatherCache.Temperature = cached.Temperature
	weatherCache.Condition = cached.Condition
	weatherCache.Humidity = cached.Humidity
	weatherCache.Updated = cached.Updated
	weatherMu.Unlock()

	age := time.Since(cached.Updated)
	if age > weatherUpdateInterval {
		log.Printf("Loaded stale weather cache (%v old): %s, %d°C (%s)", age.Round(time.Minute), cached.Location, cached.Temperature, cached.Condition)
	} else {
		log.Printf("Loaded fresh weather cache: %s, %d°C (%s)", cached.Location, cached.Temperature, cached.Condition)
	}

	return true
}

// saveWeatherCache atomically saves to disk
func saveWeatherCache() error {
	weatherMu.RLock()
	defer weatherMu.RUnlock()

	cacheDir := "data"
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	cached := struct {
		Location    string    `json:"location"`
		Temperature int       `json:"temperature"`
		Condition   string    `json:"condition"`
		Humidity    int       `json:"humidity"`
		Updated     time.Time `json:"updated"`
	}{
		Location:    weatherCache.Location,
		Temperature: weatherCache.Temperature,
		Condition:   weatherCache.Condition,
		Humidity:    weatherCache.Humidity,
		Updated:     weatherCache.Updated,
	}

	jsonBytes, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		return err
	}

	tempFile := weatherCacheFile + ".tmp"
	if err := os.WriteFile(tempFile, jsonBytes, 0644); err != nil {
		return err
	}
	return os.Rename(tempFile, weatherCacheFile)
}

// updateWeatherCache fetches and updates the cache
func updateWeatherCache() {
	loc, temp, cond, hum, err := fetchWeatherFromAPI()
	if err != nil {
		log.Printf("Weather update failed: %v", err)
		return
	}

	weatherMu.Lock()
	weatherCache.Location = loc
	weatherCache.Temperature = temp
	weatherCache.Condition = cond
	weatherCache.Humidity = hum
	weatherCache.Updated = time.Now()
	weatherMu.Unlock()

	if err := saveWeatherCache(); err != nil {
		log.Printf("Failed to save weather cache: %v", err)
	} else {
		log.Printf("Weather updated: %s, %d°C (%s)", loc, temp, cond)
	}
}

// startWeatherUpdater runs periodic weather updates in background
func startWeatherUpdater(ctx context.Context) {
	ticker := time.NewTicker(weatherUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Weather updater stopped")
			return
		case <-ticker.C:
			updateWeatherCache()
		}
	}
}

func getWeather() (string, int, string, int, error) {
	weatherMu.RLock()
	defer weatherMu.RUnlock()

	if weatherCache.Location == "" {
		return weatherLocation, 0, "loading...", 0, nil
	}
	return weatherCache.Location, weatherCache.Temperature, weatherCache.Condition, weatherCache.Humidity, nil
}

func handleStatic(tpl, fragmentTpl *template.Template) http.HandlerFunc {
	rootFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Printf("Error: Failed to load FS: %v", err)
	}
	fileServer := http.FileServer(http.FS(rootFS))

	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".tmpl") || r.URL.Path == "/index.html" {
			http.NotFound(w, r)
			return
		}

		switch r.URL.Path {
		case "/":
			status := getStatusJSON()
			jsonBytes, _ := json.MarshalIndent(status, "", "  ")

			data := struct {
				StatusJSON template.HTML
				Footer     struct {
					BuildTimestamp string
					CommitHash     string
					Year           string
				}
			}{
				StatusJSON: template.HTML(jsonBytes),
				Footer: struct {
					BuildTimestamp string
					CommitHash     string
					Year           string
				}{
					BuildTimestamp: buildTimestamp,
					CommitHash:     commit,
					Year:           year,
				},
			}

			w.Header().Set("Cache-Control", "no-store")
			if err := tpl.Execute(w, data); err != nil {
				log.Printf("Template error: %v", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}

		case "/status-fragment":
			status := getStatusJSON()
			jsonBytes, err := json.MarshalIndent(status, "", "  ")
			if err != nil {
				http.Error(w, "JSON marshal error", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fragmentTpl.Execute(w, template.HTML(jsonBytes))

		default:
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			fileServer.ServeHTTP(w, r)
		}
	}
}

func handlePprof(w http.ResponseWriter, req *http.Request) {
	getPprof := pprof.Lookup("heap")
	err := getPprof.WriteTo(w, 1)
	if err != nil {
		log.Printf("Error: Failed to write pprof: %v", err)
	}
}

func getStatusJSON() StatusJSON {
	info, err := host.Info()
	if err != nil {
		log.Printf("host.Info error: %v", err)
		info = &host.InfoStat{}
	}

	uptimeStr := formatUptime(info.Uptime)
	hostEntry := HostEntry{
		Hostname: info.Hostname,
		Uptime:   uptimeStr,
	}
	hostEntry.Kernel.Arch = info.KernelArch
	hostEntry.Kernel.Version = info.KernelVersion

	status := StatusJSON{
		Host: []HostEntry{hostEntry},
	}

	loc, temp, cond, hum, err := getWeather()
	if err != nil {
		log.Printf("Weather fetch error (using empty): %v", err)
	} else {
		status.Weather = struct {
			Location    string `json:"location"`
			Temperature int    `json:"temperature"`
			Condition   string `json:"condition"`
			Humidity    int    `json:"humidity"`
		}{loc, temp, cond, hum}
	}

	return status
}

func gracefulShutdown(server *http.Server, timeout time.Duration, cancelWeather context.CancelFunc) error {
	done := make(chan error, 1)
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
		<-c
		log.Println("Server is shutting down...")

		cancelWeather()

		ctx := context.Background()
		var cancel context.CancelFunc
		if timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		done <- server.Shutdown(ctx)
	}()

	log.Println("Starting HTTP server on :8080...")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	log.Println("Byeee")
	return <-done
}

func main() {
	log.Println("Initializing server...")

	// Step 1: Try to load existing cache
	cacheLoaded := loadWeatherCache()

	// Step 2: If no cache or stale, fetch immediately
	if !cacheLoaded {
		log.Println("No valid cache found, fetching weather...")
		updateWeatherCache()
	} else {
		// Check if cache is stale
		weatherMu.RLock()
		cacheAge := time.Since(weatherCache.Updated)
		weatherMu.RUnlock()

		if cacheAge > weatherUpdateInterval {
			log.Println("Cache is stale, fetching fresh weather...")
			updateWeatherCache()
		}
	}

	// Step 3: Start periodic weather updater in background
	ctx, cancel := context.WithCancel(context.Background())
	go startWeatherUpdater(ctx)

	// Step 4: Setup HTTP server
	router := http.NewServeMux()

	tpl, err := template.ParseFS(staticFiles, "static/index.html")
	if err != nil {
		log.Fatalf("Error parsing template: %s", err)
	}

	fragmentTpl := template.Must(template.New("fragment").Parse(
		`<pre id="uptime-status" tabindex="1" class="uptime">{{ . }}</pre>`))

	router.Handle("/", handleStatic(tpl, fragmentTpl))
	router.HandleFunc("/pprof", handlePprof)

	server := &http.Server{
		Addr:         ":8080",
		Handler:      router,
		IdleTimeout:  60 * time.Second,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	if err := gracefulShutdown(server, 10*time.Second, cancel); err != nil {
		log.Println(err)
	}
}
