package logger

import (
	"log"
	"os"
)

func Init() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}
