package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/singbox"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/web"
)

func main() {
	var (
		addr    = flag.String("addr", ":9091", "адрес HTTP-сервера")
		install = flag.Bool("install", false, "только установить sing-box и выйти")
	)
	flag.Parse()

	fmt.Println("=== Keenetic Sing-box Manager ===")

	if err := singbox.EnsureInstalled(); err != nil {
		fmt.Fprintf(os.Stderr, "ОШИБКА singbox: %v\n", err)
		os.Exit(1)
	}

	if *install {
		fmt.Println("[main] установка завершена, выходим (--install)")
		return
	}

	if err := web.Start(*addr); err != nil {
		fmt.Fprintf(os.Stderr, "ОШИБКА web: %v\n", err)
		os.Exit(1)
	}
}
