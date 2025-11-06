package client

import (
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/mooncorn/dockyard/proto/pb"
	"github.com/shirou/gopsutil/v3/mem"
)

// CollectSystemMetadata gathers worker system specifications
func CollectSystemMetadata() (*pb.WorkerMetadata, error) {
	// Get hostname
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("failed to get hostname: %w", err)
	}

	// Get IP address (prefer non-loopback IPv4)
	ipAddress := getOutboundIP()

	// Get CPU cores
	cpuCores := runtime.NumCPU()

	// Get RAM in MB
	ramMB, err := getRealRAM()
	if err != nil {
		return nil, fmt.Errorf("failed to get RAM: %w", err)
	}

	return &pb.WorkerMetadata{
		Hostname:  hostname,
		IpAddress: ipAddress,
		CpuCores:  int32(cpuCores),
		RamMb:     int32(ramMB),
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

// getRealRAM gets the actual total system RAM in MB
func getRealRAM() (int, error) {
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return 0, fmt.Errorf("failed to get virtual memory stats: %w", err)
	}

	// Convert bytes to MB
	ramMB := int(vmStat.Total / (1024 * 1024))
	return ramMB, nil
}
