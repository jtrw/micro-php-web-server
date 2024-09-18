package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jessevdk/go-flags"
	fcgiclient "github.com/tomasen/fcgi_client"
)

type Options struct {
	Config         string        `short:"c" long:"config" env:"CONFIG" default:"config.yml" description:"config file"`
	Listen         string        `short:"l" long:"listen" env:"LISTEN_SERVER" default:":8080" description:"listen address"`
	Secret         string        `short:"s" long:"secret" env:"SECRET_KEY" default:"123"`
	PinSize        int           `long:"pinszie" env:"PIN_SIZE" default:"5" description:"pin size"`
	MaxExpire      time.Duration `long:"expire" env:"MAX_EXPIRE" default:"24h" description:"max lifetime"`
	MaxPinAttempts int           `long:"pinattempts" env:"PIN_ATTEMPTS" default:"3" description:"max attempts to enter pin"`
	WebRoot        string        `long:"web" env:"WEB" default:"/app/public" description:"web ui location"`
	IndexFile      string        `long:"index" env:"INDEX_FILE" default:"index.php" description:"index file"`
	Dbg            bool          `long:"dbg" env:"DEBUG" description:"show debug info"`
	HttpTimeout    time.Duration `long:"http-timeout" env:"HTTP_TIMEOUT" default:"30s" description:"http client timeout"`
}

var revision string

func main() {

	log.Printf("[INFO] Micro tracker praser: %s\n", revision)

	var opts Options
	parser := flags.NewParser(&opts, flags.Default)
	_, err := parser.Parse()
	if err != nil {
		log.Printf("[FATAL] %v", err)
		os.Exit(1)
	}

	// Налаштування
	root := opts.WebRoot
	indexFile := opts.IndexFile
	maxBodySize := int64(8 << 20) // 8MB

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Обмеження розміру тіла запиту
		r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

		// Встановлення кодування
		w.Header().Set("Charset", "utf-8")

		// Обробка спеціальних маршрутів
		switch r.URL.Path {
		case "/favicon.ico":
			// Відключення логування та повернення 404
			w.WriteHeader(http.StatusNotFound)
			return

		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("User-agent: *\nDisallow: /\n"))
			return
		}

		// Обробка статичних файлів
		if isStaticFile(r.URL.Path) {
			staticFilePath := filepath.Join(root, r.URL.Path)
			if fileExists(staticFilePath) {
				http.ServeFile(w, r, staticFilePath)
				return
			} else {
				// Перенаправлення на @rewriteapp (в даному випадку, просто продовжуємо)
			}
		}

		// Обробка основного маршруту з try_files
		servePath := r.URL.Path
		tryFiles := []string{
			servePath,
			servePath + "/",
			"/" + indexFile,
		}

		for _, path := range tryFiles {
			fullPath := filepath.Join(root, path)
			if fileExists(fullPath) {
				// Якщо це PHP файл
				if strings.HasSuffix(fullPath, ".php") {
					servePHP(w, r, fullPath, root)
					return
				} else {
					http.ServeFile(w, r, fullPath)
					return
				}
			}
		}

		// Якщо нічого не знайдено, повертаємо 404
		http.NotFound(w, r)
	})

	// Запуск сервера
	log.Println("Сервер запущено на порту :80")
	if err := http.ListenAndServe(":80", handler); err != nil {
		log.Fatal(err)
	}
}

// Перевірка, чи існує файл
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Перевірка, чи є файл статичним ресурсом
func isStaticFile(path string) bool {
	extensions := []string{".js", ".css", ".png", ".jpeg", ".jpg", ".gif", ".ico", ".swf", ".flv", ".pdf", ".zip"}
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range extensions {
		if ext == e {
			return true
		}
	}
	return false
}

// Функція для обробки PHP файлів через FastCGI
func servePHP(w http.ResponseWriter, r *http.Request, scriptPath string, documentRoot string) {
	// Перевірка, чи файл існує
	if !fileExists(scriptPath) {
		http.NotFound(w, r)
		return
	}

	// Підключення до php-fpm
	fcgi, err := fcgiclient.Dial("tcp", "127.0.0.1:9000") // Або використовуйте "unix" і шлях до сокету
	if err != nil {
		http.Error(w, "Не вдалося підключитися до php-fpm", http.StatusInternalServerError)
		return
	}
	defer fcgi.Close()

	// Налаштування параметрів FastCGI
	params := map[string]string{
		"SCRIPT_FILENAME":   scriptPath,
		"SCRIPT_NAME":       r.URL.Path,
		"REQUEST_METHOD":    r.Method,
		"QUERY_STRING":      r.URL.RawQuery,
		"CONTENT_TYPE":      r.Header.Get("Content-Type"),
		"CONTENT_LENGTH":    r.Header.Get("Content-Length"),
		"DOCUMENT_ROOT":     documentRoot,
		"SERVER_PROTOCOL":   r.Proto,
		"REMOTE_ADDR":       r.RemoteAddr,
		"REQUEST_URI":       r.RequestURI,
		"GATEWAY_INTERFACE": "CGI/1.1",
		"SERVER_SOFTWARE":   "go/fcgi",
		"SERVER_NAME":       r.Host,
		"SERVER_PORT":       "80",
	}

	// Додавання HTTP заголовків до параметрів
	for k, v := range r.Header {
		key := "HTTP_" + strings.ToUpper(strings.ReplaceAll(k, "-", "_"))
		params[key] = v[0]
	}

	// Виконання запиту до php-fpm
	resp, err := fcgi.Request(params, r.Body)
	if err != nil {
		http.Error(w, "Помилка обробки запиту", http.StatusInternalServerError)
		return
	}

	// Передача заголовків відповіді клієнту
	for k, v := range resp.Header {
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}

	// Встановлення статус-коду відповіді
	//w.WriteHeader(resp.StatusCode)

	// Передача тіла відповіді клієнту
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Println("Помилка передачі відповіді:", err)
	}
}
