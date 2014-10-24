package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"log/syslog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	irc "github.com/fluffle/goirc/client"
)

const CONN_RETRY_DELAY = 3

var (
	useSyslog      = flag.Bool("syslog", false, "Log to syslog")
	nick           = flag.String("nick", "parrot", "bot's nickname")
	nickPassword   = flag.String("nickpassword", "", "nickserv password")
	ircAddress     = flag.String("irc-address", "irc.freenode.net", "IRC server address")
	ircSSL         = flag.Bool("ssl", false, "Connect with SSL")
	defaultChannel = flag.String("default-channel", "parrot", "default channel for messages, and initial channel")
	httpAddress    = flag.String("http-address", ":5555", "TCP address of the HTTP server")
)

// The struct going from the HTTP go routine to the IRC channel by the Bridge chan
type ChannelMessage struct {
	Channel string
	Message []byte
}

type IRCBridge struct {
	Client     *irc.Conn
	Bridge     chan ChannelMessage
	IrcAddress string
}

func (irc *IRCBridge) Channels() []string {
	return irc.Client.Me().ChannelsStr()
}

// goroutine blocking on receiving messages and emitting them to the appropriate chan
func (irc *IRCBridge) recv() {
	for {
		msg := <-irc.Bridge
		channel := fmt.Sprintf("#%s", msg.Channel)
		for _, line := range bytes.Split(msg.Message, []byte("\n")) {
			strMsg := fmt.Sprintf("%s", line)
			irc.Emit(channel, strMsg)
		}
	}
}

func (irc *IRCBridge) Emit(channel string, message string) {
	// join channels we don't track
	if _, isOn := irc.Client.StateTracker().IsOn(channel, irc.Client.Me().Nick); !isOn {
		log.Println("Joining", channel)
		irc.Client.Join(channel)
	}
	irc.Client.Privmsg(channel, message)
}

func (irc *IRCBridge) ReceiveHTTPMessage(w http.ResponseWriter, r *http.Request, channel string) {
	var msg []byte
	if r.Method != "POST" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ct := r.Header.Get("Content-Type")
	if ct == "application/x-www-form-urlencoded" || ct == "multipart/form-data" {
		msg = []byte(strings.TrimSpace(r.FormValue("msg")))
		if len(msg) == 0 {
			return
		}

	} else {
		var err error
		msg, err = ioutil.ReadAll(r.Body)
		if err != nil {
			fmt.Fprintf(w, "POST error in body reading: %s", err)
			return
		}
	}

	// Can't acknowledge this message
	if !irc.Client.Connected() {
		log.Printf("Couldn't send '%s' to channel %s on behalf of %s",
			bytes.Replace(msg, []byte("\n"), []byte("\\n"), -1),
			channel,
			r.RemoteAddr)
		w.Header().Set("Retry-After", fmt.Sprintf("%d", CONN_RETRY_DELAY*2))
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	log.Printf("%s sent '%s' to channel %s",
		r.RemoteAddr,
		bytes.Replace(msg, []byte("\n"), []byte("\\n"), -1),
		channel)
	irc.Bridge <- ChannelMessage{channel, msg}
}

func (irc *IRCBridge) connectRetry() {
	for err := irc.connect(); err != nil; {
		time.Sleep(CONN_RETRY_DELAY * time.Second)
		err = irc.connect()
	}
}

func (irc *IRCBridge) connect() (err error) {
	log.Printf("Connecting to IRC %s", irc.IrcAddress)

	irc.Client.Config().Server = irc.IrcAddress
	if *ircSSL {
		irc.Client.Config().SSL = true
		irc.Client.Config().SSLConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if err = irc.Client.Connect(); err != nil {
		log.Printf("Connection error: %s\n", err)
	}
	return
}

func main() {
	flag.Parse()

	// Find our URL
	hostname, _ := os.Hostname()
	re, _ := regexp.Compile(`:(\d)+$`)
	port := re.Find([]byte(*httpAddress))
	url := fmt.Sprintf("http://%s%s", hostname, port)

	if *useSyslog {
		sl, err := syslog.New(syslog.LOG_INFO, "parrot")
		if err != nil {
			log.Fatalf("Can't initialize syslog: %v", err)
		}
		log.SetOutput(sl)
		log.SetFlags(0)
	}

	filename := "home.html"
	t, err := template.ParseFiles(filename)
	if err != nil {
		panic(err)
	}

	// create new IRC connection
	c := irc.SimpleClient(*nick, *nick)

	parrot := IRCBridge{c, make(chan ChannelMessage), *ircAddress}

	// keep track of channels we're on (and much more we don't need)
	c.EnableStateTracking()

	c.HandleFunc("connected",
		func(conn *irc.Conn, line *irc.Line) {
			conn.Join(fmt.Sprintf("#%s", *defaultChannel))
			log.Printf("Connected")
			if len(*nickPassword) > 0 {
				conn.Privmsg("NickServ", "IDENTIFY "+*nickPassword)
			}
		})
	c.HandleFunc("disconnected",
		func(conn *irc.Conn, line *irc.Line) {
			conn.Join(fmt.Sprintf("#%s", *defaultChannel))
			log.Printf("Oops got disconnected, retrying to connect...")
			go parrot.connectRetry()
		})

	c.HandleFunc("NOTICE",
		func(conn *irc.Conn, line *irc.Line) {
			log.Printf("NOTICE: %s", line.Raw)
		})

	c.HandleFunc("PRIVMSG",
		func(conn *irc.Conn, line *irc.Line) {
			channel := line.Args[0]
			message := line.Args[1]
			standardDisclaimer := fmt.Sprintf("I'm not very smart, see %s", url)
			r, err := regexp.Compile(fmt.Sprintf("(?i:%s|%s|parrot)(?::|,)",
				regexp.QuoteMeta(*nick),
				regexp.QuoteMeta(conn.Me().Nick)))
			if err != nil {
				log.Printf("err: %s, %s\n", conn.Me().Nick, err)
				return
			}
			if channel == conn.Me().Nick {
				log.Printf("%s said to me %s: %s\n", line.Nick, channel, message)
				conn.Privmsg(line.Nick, standardDisclaimer)
			} else if r.MatchString(message) {
				log.Printf("%s said to me %s: %s\n", line.Nick, channel, message)
				conn.Privmsg(channel, standardDisclaimer)
			}
		})

	// Print a small SYNOPSIS on home page of the HTTP server
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		home := struct {
			Nick        string
			Channels    []string
			Url         string
			HttpAddress string
			IrcAddress  string
		}{
			parrot.Client.Me().Nick,
			parrot.Channels(),
			url,
			*httpAddress,
			parrot.IrcAddress,
		}
		t.Execute(w, home)
	})

	// Message handlers
	http.HandleFunc("/post/", func(w http.ResponseWriter, r *http.Request) {
		lenPath := len("/post/")
		channel := r.URL.Path[lenPath:]
		if len(strings.TrimSpace(channel)) == 0 {
			channel = *defaultChannel
		}
		parrot.ReceiveHTTPMessage(w, r, channel)
	})
	http.HandleFunc("/post", func(w http.ResponseWriter, r *http.Request) {
		parrot.ReceiveHTTPMessage(w, r, *defaultChannel)
	})

	// start receiver
	go parrot.recv()

	// connect to irc server
	ircerr := parrot.connect()
	if ircerr != nil {
		os.Exit(1)
	}

	log.Printf("HTTP server running at %s", *httpAddress)
	httpErr := http.ListenAndServe(*httpAddress, nil)
	if httpErr != nil {
		log.Fatalf("HTTP error: %s", httpErr)
	}
}
