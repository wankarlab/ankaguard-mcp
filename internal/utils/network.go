package utils

import (
	"fmt"
	"net"
	"os"
)

// GetLocalIP obtiene la IP local de la máquina
// Prioriza la variable de entorno SERVER_IP si está configurada
func GetLocalIP() string {
	// Primero intentar obtener de variable de entorno
	if ip := os.Getenv("SERVER_IP"); ip != "" {
		return ip
	}

	// Si no hay variable de entorno, detectar automáticamente
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "0.0.0.0"
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}

	return "0.0.0.0"
}

// GetServerAddress construye la dirección completa del servidor
// Formato: IP:PORT
func GetServerAddress(port int) string {
	ip := GetLocalIP()
	return fmt.Sprintf("%s:%d", ip, port)
}

// GetServerURL construye la URL completa del servidor
// Formato: http://IP:PORT
func GetServerURL(port int) string {
	ip := GetLocalIP()
	return fmt.Sprintf("http://%s:%d", ip, port)
}

// GetGRPCAddress obtiene la dirección de GoCryptoTrader
// Prioriza la variable de entorno GCT_ADDRESS
func GetGRPCAddress() string {
	if addr := os.Getenv("GCT_ADDRESS"); addr != "" {
		return addr
	}

	// Por defecto usar localhost para GCT ya que corre en la misma máquina
	return "localhost:9052"
}
