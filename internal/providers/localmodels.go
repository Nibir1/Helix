// internal/providers/localmodels.go
// Purpose: Hardware detection and local model recommendations.
package providers

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// HardwareProfile describes the local machine.
type HardwareProfile struct {
	OS         string
	Arch       string
	RAMGB      int
	GPU        bool
	GPUName    string
	FreeDiskGB int
}

// LocalModelRecommendation describes a recommended local model.
type LocalModelRecommendation struct {
	ID          string
	DisplayName string
	Runtime     string
	OllamaTag   string
	GGUFURL     string
	SHA256      string
	MinRAMGB    int
	Reason      string
}

// DetectHardware detects CPU/RAM/GPU/disk heuristically.
func DetectHardware() HardwareProfile {
	profile := HardwareProfile{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}

	profile.RAMGB = detectRAMGB()
	profile.GPU, profile.GPUName = detectGPU()
	profile.FreeDiskGB = detectFreeDiskGB()

	return profile
}

// RecommendLocalModels returns local model recommendations.
func RecommendLocalModels(h HardwareProfile) []LocalModelRecommendation {
	if h.RAMGB <= 0 {
		h.RAMGB = 8
	}

	all := []LocalModelRecommendation{
		{
			ID:          "tinyllama-1-1b",
			DisplayName: "TinyLlama 1.1B Q4 (very low RAM)",
			Runtime:     "llamacpp",
			GGUFURL:     "https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF/resolve/main/tinyllama-1.1b-chat-v1.0.Q4_0.gguf",
			SHA256:      "da3087fb14aede55fde6eb81a0e55e886810e43509ec82ecdc7aa5d62a03b556",
			MinRAMGB:    4,
			Reason:      "Fast fallback for low-memory systems.",
		},
		{
			ID:          "phi4-mini",
			DisplayName: "Phi-4 mini (Ollama)",
			Runtime:     "ollama",
			OllamaTag:   "phi4-mini",
			MinRAMGB:    8,
			Reason:      "Strong small model for coding and planner work.",
		},
		{
			ID:          "qwen3-4b",
			DisplayName: "Qwen3 4B (Ollama)",
			Runtime:     "ollama",
			OllamaTag:   "qwen3:4b",
			MinRAMGB:    8,
			Reason:      "Good multilingual and command-generation model.",
		},
		{
			ID:          "gemma3-4b",
			DisplayName: "Gemma 3 4B (Ollama)",
			Runtime:     "ollama",
			OllamaTag:   "gemma3:4b",
			MinRAMGB:    8,
			Reason:      "Balanced small model for general assistance.",
		},
		{
			ID:          "mistral-7b",
			DisplayName: "Mistral 7B (Ollama)",
			Runtime:     "ollama",
			OllamaTag:   "mistral:7b",
			MinRAMGB:    16,
			Reason:      "Solid 7B general-purpose local model.",
		},
		{
			ID:          "llama3-1-8b",
			DisplayName: "Llama 3.1 8B (Ollama)",
			Runtime:     "ollama",
			OllamaTag:   "llama3.1:8b",
			MinRAMGB:    16,
			Reason:      "Strong 8B model when RAM allows.",
		},
		{
			ID:          "llama2-7b",
			DisplayName: "Llama 2 7B Q4 (llama.cpp)",
			Runtime:     "llamacpp",
			GGUFURL:     "https://huggingface.co/TheBloke/Llama-2-7B-Chat-GGUF/resolve/main/llama-2-7b-chat.Q4_0.gguf",
			SHA256:      "9958ee9b670594147b750bbc7d0540b928fa12dcc5dd4c58cc56ed2eb85e371b",
			MinRAMGB:    16,
			Reason:      "Stable GGUF model for llama.cpp server.",
		},
	}

	out := make([]LocalModelRecommendation, 0, len(all))

	for _, m := range all {
		if h.RAMGB >= m.MinRAMGB {
			out = append(out, m)
		}
	}

	if len(out) == 0 {
		out = append(out, all[0])
	}

	return out
}

func detectRAMGB() int {
	switch runtime.GOOS {
	case "linux":
		data, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return 8
		}

		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) < 2 {
					return 8
				}

				kb, err := strconv.Atoi(fields[1])
				if err != nil {
					return 8
				}

				return kb / 1_048_576
			}
		}

	case "darwin":
		out, err := runOutput(5*time.Second, "sysctl", "-n", "hw.memsize")
		if err == nil {
			bytes, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
			if err == nil {
				return int(bytes / (1024 * 1024 * 1024))
			}
		}

	case "windows":
		out, err := runOutput(10*time.Second, "wmic", "computersystem", "get", "TotalPhysicalMemory", "/value")
		if err == nil {
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "TotalPhysicalMemory=") {
					raw := strings.TrimPrefix(line, "TotalPhysicalMemory=")
					bytes, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
					if err == nil {
						return int(bytes / (1024 * 1024 * 1024))
					}
				}
			}
		}
	}

	return 8
}

func detectGPU() (bool, string) {
	if runtime.GOOS == "darwin" {
		return true, "Metal"
	}

	if path, err := exec.LookPath("nvidia-smi"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		out, err := exec.CommandContext(ctx, path, "-L").Output()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return true, strings.TrimSpace(strings.Split(string(out), "\n")[0])
		}
	}

	if path, err := exec.LookPath("rocm-smi"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := exec.CommandContext(ctx, path).Run()
		if err == nil {
			return true, "ROCm"
		}
	}

	return false, ""
}

func detectFreeDiskGB() int {
	switch runtime.GOOS {
	case "windows":
		drive := os.Getenv("SystemDrive")
		if drive == "" {
			drive = "C:"
		}

		out, err := runOutput(10*time.Second, "wmic", "logicaldisk", "where", "DeviceID='"+drive+"'", "get", "FreeSpace", "/value")
		if err == nil {
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "FreeSpace=") {
					raw := strings.TrimPrefix(line, "FreeSpace=")
					free, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
					if err == nil {
						return int(free / (1024 * 1024 * 1024))
					}
				}
			}
		}

	default:
		out, err := runOutput(5*time.Second, "df", "-Pk", ".")
		if err == nil {
			lines := strings.Split(strings.TrimSpace(out), "\n")
			if len(lines) >= 2 {
				fields := strings.Fields(lines[1])
				if len(fields) >= 4 {
					availKB, err := strconv.Atoi(fields[3])
					if err == nil {
						return availKB / 1_048_576
					}
				}
			}
		}
	}

	return 0
}

func runOutput(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", err
	}

	return string(out), nil
}
