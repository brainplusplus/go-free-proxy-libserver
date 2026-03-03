// package portutil provides cross-platform utilities for managing ports
package util

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// EnsureAvailable checks if a port is available. If not, it attempts to kill
// any process listening on that port. Returns an error if the port cannot be freed.
func EnsureAvailable(port int) error {
	if isAvailable(port) {
		return nil
	}

	fmt.Printf("[portutil] Port %d is in use. Attempting to free it...\n", port)

	if err := killProcessOnPort(port); err != nil {
		return fmt.Errorf("failed to free port %d: %w", port, err)
	}

	// Wait for port to be released
	time.Sleep(500 * time.Millisecond)

	if !isAvailable(port) {
		return fmt.Errorf("port %d still in use after killing process", port)
	}

	fmt.Printf("[portutil] Port %d is now available\n", port)
	return nil
}

// isAvailable checks if a port can be bound by attempting to dial it
// If we can connect, something is listening
func isAvailable(port int) bool {
	// First, try to connect - if connection succeeds, port is in use
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err == nil {
		conn.Close()
		return false // Port is in use
	}

	// Second, try to listen - if this fails, port is in use
	ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// killProcessOnPort finds and kills the process listening on the given port
func killProcessOnPort(port int) error {
	switch runtime.GOOS {
	case "windows":
		return killProcessOnPortWindows(port)
	case "linux", "darwin":
		return killProcessOnPortUnix(port)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func killProcessOnPortWindows(port int) error {
	// netstat -ano | findstr :PORT | findstr LISTENING
	cmd := exec.Command("cmd", "/c",
		fmt.Sprintf("netstat -ano | findstr :%d | findstr LISTENING", port))
	output, err := cmd.Output()
	if err != nil {
		return nil // No process found, port is free
	}

	// Parse output: "  TCP    0.0.0.0:15000    0.0.0.0:0    LISTENING    12345"
	fields := strings.Fields(string(output))
	if len(fields) < 5 {
		return nil
	}

	pid := fields[len(fields)-1]

	// Kill the process
	killCmd := exec.Command("taskkill", "/PID", pid, "/F")
	if err := killCmd.Run(); err != nil {
		return fmt.Errorf("taskkill PID %s failed: %w", pid, err)
	}

	fmt.Printf("[portutil] Killed PID %s on port %d\n", pid, port)
	return nil
}

func killProcessOnPortUnix(port int) error {
	// Try lsof first
	cmd := exec.Command("lsof", "-t", "-i", fmt.Sprintf(":%d", port))
	output, err := cmd.Output()
	if err == nil {
		pid := strings.TrimSpace(string(output))
		if pid != "" {
			killCmd := exec.Command("kill", "-9", pid)
			if err := killCmd.Run(); err != nil {
				return fmt.Errorf("kill PID %s failed: %w", pid, err)
			}
			fmt.Printf("[portutil] Killed PID %s on port %d\n", pid, port)
			return nil
		}
	}

	// Fallback to fuser
	cmd = exec.Command("fuser", "-k", fmt.Sprintf("%d/tcp", port))
	if err := cmd.Run(); err != nil {
		return nil // fuser may fail if no process found
	}

	fmt.Printf("[portutil] Killed process on port %d\n", port)
	return nil
}

// GetPortFromEnv reads PORT from environment, returns default if not set
func GetPortFromEnv(defaultPort int) int {
	portStr := os.Getenv("PORT")
	if portStr == "" {
		return defaultPort
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return defaultPort
	}
	return port
}
