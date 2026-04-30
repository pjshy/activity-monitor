package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFiles embed.FS

const sampleInterval = 2 * time.Second

type Process struct {
	PID     int     `json:"pid"`
	PPID    int     `json:"ppid"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Memory  float64 `json:"memory"`
	RSSKB   int64   `json:"rssKb"`
	State   string  `json:"state"`
	Command string  `json:"command"`
	Args    string  `json:"args"`
}

type Summary struct {
	OS               string  `json:"os"`
	ProcessCount     int     `json:"processCount"`
	CPU              float64 `json:"cpu"`
	MemoryUsed       float64 `json:"memoryUsed"`
	MemoryUsedBytes  uint64  `json:"memoryUsedBytes"`
	MemoryTotalBytes uint64  `json:"memoryTotalBytes"`
	LoadAverage      string  `json:"loadAverage"`
	SampledAt        string  `json:"sampledAt"`
}

type Snapshot struct {
	Summary   Summary   `json:"summary"`
	Processes []Process `json:"processes"`
	Error     string    `json:"error,omitempty"`
}

type Sampler struct {
	mu       sync.RWMutex
	snapshot Snapshot
	interval time.Duration
}

func NewSampler(interval time.Duration) *Sampler {
	return &Sampler{interval: interval}
}

func (s *Sampler) Start(ctx context.Context) {
	s.refresh(ctx)

	ticker := time.NewTicker(s.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.refresh(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Sampler) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	processes := make([]Process, len(s.snapshot.Processes))
	copy(processes, s.snapshot.Processes)
	snapshot := s.snapshot
	snapshot.Processes = processes
	return snapshot
}

func (s *Sampler) refresh(ctx context.Context) {
	snapshot := collectSnapshot(ctx)

	s.mu.Lock()
	s.snapshot = snapshot
	s.mu.Unlock()
}

func collectSnapshot(ctx context.Context) Snapshot {
	processes, err := collectProcesses(ctx)
	if err != nil {
		return Snapshot{
			Summary: Summary{
				OS:        runtime.GOOS,
				SampledAt: time.Now().UTC().Format(time.RFC3339),
			},
			Error: err.Error(),
		}
	}

	sort.Slice(processes, func(i, j int) bool {
		if processes[i].CPU == processes[j].CPU {
			return processes[i].Memory > processes[j].Memory
		}
		return processes[i].CPU > processes[j].CPU
	})

	summary := buildSummary(ctx, processes)
	return Snapshot{Summary: summary, Processes: processes}
}

func collectProcesses(ctx context.Context) ([]Process, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, "ps",
		"-A",
		"-o", "pid=",
		"-o", "ppid=",
		"-o", "user=",
		"-o", "pcpu=",
		"-o", "pmem=",
		"-o", "rss=",
		"-o", "state=",
		"-o", "command=",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("collect processes: %w", err)
	}

	return parseProcessList(string(out))
}

func parseProcessList(output string) ([]Process, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	processes := make([]Process, 0, 256)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		cpu, _ := strconv.ParseFloat(fields[3], 64)
		memory, _ := strconv.ParseFloat(fields[4], 64)
		rss, _ := strconv.ParseInt(fields[5], 10, 64)

		commandLine := strings.Join(fields[7:], " ")
		processes = append(processes, Process{
			PID:     pid,
			PPID:    ppid,
			User:    fields[2],
			CPU:     round(cpu, 2),
			Memory:  round(memory, 2),
			RSSKB:   rss,
			State:   fields[6],
			Command: commandName(commandLine),
			Args:    commandLine,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan processes: %w", err)
	}
	if len(processes) == 0 {
		return nil, errors.New("no process data returned by ps")
	}

	return processes, nil
}

func commandName(commandLine string) string {
	if commandLine == "" {
		return ""
	}
	return strings.Fields(commandLine)[0]
}

func buildSummary(ctx context.Context, processes []Process) Summary {
	var cpu float64
	for _, process := range processes {
		cpu += process.CPU
	}

	total, used := collectMemory(ctx)
	memoryUsed := 0.0
	if total > 0 {
		memoryUsed = float64(used) / float64(total) * 100
	}

	return Summary{
		OS:               runtime.GOOS,
		ProcessCount:     len(processes),
		CPU:              round(cpu, 2),
		MemoryUsed:       round(memoryUsed, 2),
		MemoryUsedBytes:  used,
		MemoryTotalBytes: total,
		LoadAverage:      collectLoadAverage(ctx),
		SampledAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

func collectMemory(ctx context.Context) (uint64, uint64) {
	if runtime.GOOS == "darwin" {
		return collectDarwinMemory(ctx)
	}
	return collectProcMemory()
}

func collectDarwinMemory(ctx context.Context) (uint64, uint64) {
	totalOut, err := runCommand(ctx, "sysctl", "-n", "hw.memsize")
	if err != nil {
		return 0, 0
	}
	total, err := strconv.ParseUint(strings.TrimSpace(totalOut), 10, 64)
	if err != nil {
		return 0, 0
	}

	vmOut, err := runCommand(ctx, "vm_stat")
	if err != nil {
		return total, 0
	}

	pageSize := uint64(4096)
	freePages := uint64(0)
	scanner := bufio.NewScanner(strings.NewReader(vmOut))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "page size of") {
			for _, field := range strings.Fields(line) {
				if n, err := strconv.ParseUint(field, 10, 64); err == nil {
					pageSize = n
					break
				}
			}
			continue
		}

		if strings.HasPrefix(line, "Pages free:") || strings.HasPrefix(line, "Pages speculative:") {
			value := strings.Trim(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]), ".")
			pages, err := strconv.ParseUint(value, 10, 64)
			if err == nil {
				freePages += pages
			}
		}
	}

	free := freePages * pageSize
	if free > total {
		return total, 0
	}
	return total, total - free
}

func collectProcMemory() (uint64, uint64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}

	values := map[string]uint64{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) < 2 {
			continue
		}
		value, err := strconv.ParseUint(parts[1], 10, 64)
		if err == nil {
			values[strings.TrimSuffix(parts[0], ":")] = value * 1024
		}
	}

	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total == 0 || available > total {
		return 0, 0
	}
	return total, total - available
}

func collectLoadAverage(ctx context.Context) string {
	if runtime.GOOS == "darwin" {
		out, err := runCommand(ctx, "sysctl", "-n", "vm.loadavg")
		if err == nil {
			return strings.Trim(strings.TrimSpace(out), "{}")
		}
	}

	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "n/a"
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return "n/a"
	}
	return strings.Join(fields[:3], " ")
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()

	out, err := exec.CommandContext(commandCtx, name, args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func round(value float64, decimals int) float64 {
	shift := math.Pow(10, float64(decimals))
	return math.Round(value*shift) / shift
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sampler := NewSampler(sampleInterval)
	sampler.Start(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/processes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, sampler.Snapshot())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	staticFS, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatalf("static filesystem: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	addr := ":8080"
	if value := strings.TrimSpace(os.Getenv("ADDR")); value != "" {
		addr = value
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("process monitor listening on http://localhost%s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
