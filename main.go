package main

import (
	"flag"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"livechat/chat"

	"github.com/joho/godotenv"
)

var Info = log.New(os.Stdout, "\u001b[34mINFO: \u001B[0m", log.LstdFlags|log.Lshortfile)
var Warning = log.New(os.Stdout, "\u001b[33mWARNING: \u001B[0m", log.LstdFlags|log.Lshortfile)
var Error = log.New(os.Stdout, "\u001b[31mERROR: \u001b[0m", log.LstdFlags|log.Lshortfile)
var Debug = log.New(os.Stdout, "\u001b[36mDEBUG: \u001B[0m", log.LstdFlags|log.Lshortfile)

func main() {
	var matrix_rypt bool
	rand.Seed(time.Now().UnixNano())

	dbfile := flag.String("dbfile", "./livematrix.db", "the SQLite DB file to use")
	dev := flag.Bool("dev", false, "Set flag to true to use development environment variables")

	log.SetFlags(log.Lshortfile)
	flag.Parse()

	// .env.prod is used by makefile to build
	envFile := ".env"
	if *dev {
		envFile = ".env.dev"
	}
	err := godotenv.Load(envFile)
	if err != nil {
		log.Fatal("Error loading .env file. Does it exist?")
	}

	// Configuration file parsing from .env
	db_type := os.Getenv("DATABASE_TYPE")
	db_pass := os.Getenv("DATABASE_PASSWORD")
	db_name := os.Getenv("DATABASE_NAME")
	db_user := os.Getenv("DATABASE_USER")
	db_ipad := os.Getenv("DATABASE_IPADDR")
	db_port := os.Getenv("DATABASE_PORT")
	server_iface := os.Getenv("SERVER_IFACE")
	server_port := os.Getenv("SERVER_PORT")
	matrix_recp := os.Getenv("MATRIX_RECIPIENT")
	matrix_user := os.Getenv("MATRIX_USERNAME")
	matrix_pass := os.Getenv("MATRIX_PASSWORD")
	matrix_srvr := os.Getenv("MATRIX_SERVER")
	matrix_time := os.Getenv("MATRIX_TIMEOUT")
	matrix_enc := os.Getenv("MATRIX_ENCRYPTED")
	if matrix_enc == "true" || matrix_enc == "True" {
		matrix_rypt = true
	} else {
		matrix_rypt = false
	}

	// Connect to database, no need to defer
	db, err := chat.ConnectSQL(db_user, db_pass, db_name, db_ipad, db_port, db_type, *dbfile)

	// Make sure to exit cleanly
	c := make(chan os.Signal, 1)
	signal.Notify(c,
		os.Interrupt,
		os.Kill,
		syscall.SIGABRT,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGTERM,
	)
	go func() {
		for range c { // when the process is killed
			log.Print("Cleaning up")
			db.GetDB().Close()
			os.Exit(0)
		}
	}()

	// If one wishes, they can move this to another file, but not database.go
	App := chat.NewApp(matrix_time, matrix_rypt, db)
	go App.Connect(matrix_recp, matrix_srvr, matrix_user, matrix_pass, matrix_rypt)

	// websocket server
	server := chat.NewServer("/entry", matrix_rypt, App)
	go server.Listen()

	// static files
	http.Handle("/", http.FileServer(http.Dir("webroot")))
	log.Fatal(http.ListenAndServe(server_iface+":"+server_port, nil))
}
