package main

import (
	"log"
	"os"
	"time"
)

func main() {
	file, err := os.Open("/opt/sre-lab/app-config.json")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	log.Printf("file-reader opened %s (fd %d)", "/opt/sre-lab/app-config.json", file.Fd())
	for {
		time.Sleep(time.Minute)
	}
}
