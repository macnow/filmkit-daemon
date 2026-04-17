// filmkit-daemon — USB-to-HTTP bridge for Fujifilm cameras.
// Serves the FilmKit frontend and a REST API that talks to the camera via PTP/USB.
package main

import (
	"flag"
	"log"
	"os"

	"filmkit-daemon/internal/api"
)

func main() {
	port := flag.Int("port", 8765, "HTTP listen port")
	frontend := flag.String("frontend", "", "Path to built FilmKit frontend directory (e.g. /www/filmkit)")
	flag.Parse()

	if *frontend == "" {
		// Default: look for frontend next to the binary
		exe, _ := os.Executable()
		*frontend = exe + "/../filmkit"
	}

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[filmkit] ")
	log.Printf("Starting filmkit-daemon (port=%d, frontend=%s)", *port, *frontend)

	srv := api.NewServer(*frontend, *port)
	if err := srv.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
