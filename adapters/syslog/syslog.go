package syslog

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"log/syslog"
	"net"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/dkiser/logspout-syslog-json/adapters/json"
	"github.com/gliderlabs/logspout/router"
)

var hostname string

func init() {
	hostname, _ = os.Hostname()
	router.AdapterFactories.Register(NewSyslogAdapter, "syslog")
}

func getopt(name, dfault string) string {
	value := os.Getenv(name)
	if value == "" {
		value = dfault
	}
	return value
}

func NewSyslogAdapter(route *router.Route) (router.LogAdapter, error) {
	transport, found := router.AdapterTransports.Lookup(route.AdapterTransport("udp"))
	if !found {
		return nil, errors.New("unable to find adapter: " + route.Adapter)
	}
	conn, err := transport.Dial(route.Address, route.Options)
	if err != nil {
		return nil, err
	}

	format := getopt("SYSLOG_FORMAT", "rfc3164")
	priority := getopt("SYSLOG_PRIORITY", "{{.Priority}}")
	hostname := getopt("SYSLOG_HOSTNAME", "{{.Container.Config.Hostname}}")
	pid := getopt("SYSLOG_PID", "{{.Container.State.Pid}}")
	tag := getopt("SYSLOG_TAG", "{{.ContainerName}}"+route.Options["append_tag"])
	structuredData := getopt("SYSLOG_STRUCTURED_DATA", "")
	json := os.Getenv("SYSLOG_JSON")
	var json_flag bool
	if json == "" {
		json_flag = false
	} else {
		json_flag = true
	}
	if route.Options["structured_data"] != "" {
		structuredData = route.Options["structured_data"]
	}

	data := getopt("SYSLOG_DATA", "{{.Data}}")

	var tmplStr string
	switch format {
	case "rfc5424":
		tmplStr = fmt.Sprintf("<%d>1 {{.Timestamp}} %s %s %d - [%s] %s\n",
			priority, hostname, tag, pid, structuredData, data)
	case "rfc3164":
		tmplStr = fmt.Sprintf("<%s>{{.Timestamp}} %s %s[%s]: %s\n",
			priority, hostname, tag, pid, data)
	default:
		return nil, errors.New("unsupported syslog format: " + format)
	}

	return &SyslogAdapter{
		route:   route,
		conn:    conn,
		tmplStr: tmplStr,
		json:    json_flag,
	}, nil
}

type SyslogAdapter struct {
	conn    net.Conn
	route   *router.Route
	tmplStr string
	json    bool
}

func (a *SyslogAdapter) Stream(logstream chan *router.Message) {
	defer a.route.Close()
	for message := range logstream {
		m := NewSyslogMessage(message, a.conn)
		if a.json {
			js, err := json.NewJSONMessage(message)
			if err != nil {
				log.Println("syslog: failed to get json, falling back:", err)
			} else {
				a.tmplStr = strings.Replace(a.tmplStr, "{{.Data}}", string(js), 1)
			}
		}

		tmpl, err := template.New("syslog").Parse(a.tmplStr)
		if err != nil {
			log.Println("syslog:", err)
			continue
		}
		buf, err := m.Render(tmpl)
		if err != nil {
			log.Println("syslog:", err)
			continue
		}
		_, err = a.conn.Write(buf.Bytes())
		if err != nil {
			log.Println("syslog:", err)
			continue
		}
	}
}

type SyslogMessage struct {
	*router.Message
	conn net.Conn
}

func NewSyslogMessage(message *router.Message, conn net.Conn) *SyslogMessage {
	return &SyslogMessage{message, conn}
}

func (m *SyslogMessage) Render(tmpl *template.Template) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	err := tmpl.Execute(buf, m)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (m *SyslogMessage) Priority() syslog.Priority {
	switch m.Message.Source {
	case "stdout":
		return syslog.LOG_USER | syslog.LOG_INFO
	case "stderr":
		return syslog.LOG_USER | syslog.LOG_ERR
	default:
		return syslog.LOG_DAEMON | syslog.LOG_INFO
	}
}

func (m *SyslogMessage) Hostname() string {
	return hostname
}

func (m *SyslogMessage) LocalAddr() string {
	return m.conn.LocalAddr().String()
}

func (m *SyslogMessage) Timestamp() string {
	return m.Message.Time.Format(time.RFC3339)
}

func (m *SyslogMessage) ContainerName() string {
	return m.Message.Container.Name[1:]
}
