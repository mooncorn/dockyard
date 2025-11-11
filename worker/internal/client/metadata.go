package client

import (
	"fmt"
	"net"
	"os"

	"github.com/mooncorn/dockyard/proto/pb"
	"github.com/mooncorn/dockyard/worker/internal/service"
)

// CollectSystemMetadata gathers worker system specifications using the resource budget
func CollectSystemMetadata(budget *service.ResourceBudget) (*pb.WorkerMetadata, error) {
	// Get hostname
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("failed to get hostname: %w", err)
	}

	// Get IP address (prefer non-loopback IPv4)
	ipAddress := getOutboundIP()

	// Get CPU and memory budget (not total system resources)
	cpuCores := budget.GetAvailableCPU()
	ramMB := budget.GetAvailableMemory()

	return &pb.WorkerMetadata{
		Hostname:  hostname,
		IpAddress: ipAddress,
		CpuCores:  float64(cpuCores),
		RamMb:     ramMB,
	}, nil
}

// getOutboundIP gets the preferred outbound IP address
func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		// Fallback to getting any non-loopback address
		addrs, err := net.InterfaceAddrs()
		if err == nil {
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						return ipnet.IP.String()
					}
				}
			}
		}
		return "unknown"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
