package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var (
	addr    = flag.String("listen", "0.0.0.0:5417", "The address to listen on")
	driver  = flag.String("driver", "postgres", "The database driver to use")
	connect = flag.String("connect", "dbname=nnev host=/var/run/postgresql sslmode=disable", "The connection string to use")
	gettpl  = flag.String("template", "/var/www/www.noname-ev.de/yarpnarp.html", "The template to serve for editing zusagen")
	hook    = flag.String("hook", "", "A hook to run on every change")

	loc  *time.Location
	tpl  *template.Template
	idRe = regexp.MustCompile(`^\d*$`)
)

type Zusage struct {
	Nick      string
	Kommt     bool
	Kommentar string
	HasKommt  bool
}

type Cookie struct {
	Nick      string
	Kommentar string
}

func writeError(errno int, res http.ResponseWriter, format string, args ...interface{}) {
	res.WriteHeader(errno)
	fmt.Fprintf(res, format, args...)
}

func YarpNarpHandler(res http.ResponseWriter, req *http.Request) {
	var err error
	tpl, err = template.New("").Delims("<<", ">>").ParseFiles(*gettpl)
	if err != nil {
		log.Fatal("Could not parse template:", err)
	}

	if req.Method == "POST" {
		handlePost(res, req)
		return
	}

	if req.Method == "GET" {
		handleGet(res, req)
		return
	}

	writeError(http.StatusMethodNotAllowed, res, "")
	return
}

func handleGet(res http.ResponseWriter, req *http.Request) {
	z := Zusage{}

	if c, _ := req.Cookie("yarpnarp"); c != nil {
		keks, err := base64.StdEncoding.DecodeString(c.Value)
		if err != nil {
			log.Println(err)
			writeError(http.StatusInternalServerError, res, "Something went wrong")
			return
		}

		cookie := Cookie{}

		if err = json.Unmarshal(keks, &cookie); err != nil {
			log.Println(err)
			writeError(http.StatusInternalServerError, res, "Something went wrong")
			return
		}
		z.Nick = strings.TrimSpace(cookie.Nick)
		z = GetZusage(z.Nick)
		if cookie.Kommentar != "" {
			z.Kommentar = cookie.Kommentar
		}
	}

	err := tpl.ExecuteTemplate(res, "yarpnarp.html", z)
	if err != nil {
		log.Println(err)
		return
	}

	return
}

func handlePost(res http.ResponseWriter, req *http.Request) {
	nick := strings.TrimSpace(req.FormValue("nick"))
	kommt := (req.FormValue("kommt") == "Yarp")
	kommentar := req.FormValue("kommentar")

	if req.FormValue("captcha") != "NoName e.V." {
		writeError(http.StatusBadRequest, res, "Captcha muss ausgefüllt werden")
		return
	}

	if nick == "" {
		writeError(http.StatusBadRequest, res, "Nick darf nicht leer sein")
		return
	}

	log.Printf("Incoming POST request: nick=\"%s\", kommt=%v, kommentar=\"%s\"\n", nick, kommt, kommentar)

	zusage := Zusage{nick, kommt, kommentar, true}

	err := zusage.Put()
	if err != nil {
		log.Printf("Could not update: %v\n", err)
		writeError(http.StatusBadRequest, res, "An error occurred.")
	}

	RunHook()

	cookie, err := json.Marshal(Cookie{Nick: nick, Kommentar: kommentar})
	if err != nil {
		log.Printf("Could not marshal: %v\n", err)
		writeError(http.StatusBadRequest, res, "An error occurred.")
		return
	}

	c := base64.StdEncoding.EncodeToString(cookie)
	http.SetCookie(res, &http.Cookie{Name: "yarpnarp", Value: c, Expires: time.Date(2030, 0, 0, 0, 0, 0, 0, time.Local)})

	http.Redirect(res, req, "/yarpnarp.html", 303)
}

func main() {
	flag.Parse()

	// We ignore SIGPIPE, because it might be generated by the hook
	// occasionally. Since we don't care for it, we don't need a buffered
	// channel
	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGPIPE)

	err := OpenDB()
	if err != nil {
		log.Fatal("Could not connect to database:", err)
	}

	tpl, err = template.New("").Delims("<<", ">>").ParseFiles(*gettpl)
	if err != nil {
		log.Fatal("Could not parse template:", err)
	}

	http.HandleFunc("/", YarpNarpHandler)

	log.Println("Listening on", *addr)
	err = http.ListenAndServe(*addr, nil)
	if err != nil {
		log.Println("Could not listen:", err)
	}
}
