package smtpd

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/opsgenie/opsgenie-go-sdk/alerts"
	ogcli "github.com/opsgenie/opsgenie-go-sdk/client"
	"google.golang.org/cloud/compute/metadata"
)

// Server is an SMTP server.
type OGServer struct {
	Addr         string        // TCP address to listen on, ":25" if empty
	Hostname     string        // optional Hostname to announce; "" to use system hostname
	ReadTimeout  time.Duration // optional read timeout
	WriteTimeout time.Duration // optional write timeout

	PlainAuth bool // advertise plain auth (assumes you're on SSL)

	// OnNewConnection, if non-nil, is called on new connections.
	// If it returns non-nil, the connection is closed.
	OnNewConnection func(c Connection) error

	// OnNewMail must be defined and is called when a new message beings.
	// (when a MAIL FROM line arrives)
	OnNewMail func(c Connection, from MailAddress) (OGEnvelopeInterface, error)
}

func (srv *OGServer) hostname() string {
	if srv.Hostname != "" {
		return srv.Hostname
	}
	out, err := exec.Command("hostname").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ListenAndServe listens on the TCP network address srv.Addr and then
// calls Serve to handle requests on incoming connections.  If
// srv.Addr is blank, ":25" is used.
func (srv *OGServer) ListenAndServe() error {
	addr := srv.Addr
	if addr == "" {
		addr = ":25"
	}
	ln, e := net.Listen("tcp", addr)
	if e != nil {
		return e
	}
	return srv.Serve(ln)
}

func (srv *OGServer) Serve(ln net.Listener) error {
	defer ln.Close()
	for {
		rw, e := ln.Accept()
		if e != nil {
			if ne, ok := e.(net.Error); ok && ne.Temporary() {
				log.Printf("smtpd: Accept error: %v", e)
				continue
			}
			return e
		}
		sess, err := srv.newSession(rw)
		if err != nil {
			continue
		}
		go sess.serve()
	}
	panic("not reached")
}

func (srv *OGServer) newSession(rwc net.Conn) (s *ogsession, err error) {
	s = &ogsession{
		srv: srv,
		rwc: rwc,
		br:  bufio.NewReader(rwc),
		bw:  bufio.NewWriter(rwc),
	}
	return
}

type OGEnvelopeInterface interface {
	AddRecipient(rcpt MailAddress) error
	AddData(string)
	BeginData() error
	SetClient(cli *ogcli.OpsGenieAlertClient)
	Write(line []byte) error
	Close() error
}

type OGEnvelope struct {
	rcpts []MailAddress
	//aggregation of sent data lines
	MsgLines    []string
	Subject     string
	Date        time.Time
	AlertUser   *string
	AlertClient *ogcli.OpsGenieAlertClient
}

//TODO: POST to http://opsgenie
func (e *OGEnvelope) AddRecipient(rcpt MailAddress) error {
	e.rcpts = append(e.rcpts, rcpt)
	return nil
}

func (e *OGEnvelope) AddData(str string) {
	e.MsgLines = append(e.MsgLines, str)
}

func (e *OGEnvelope) BeginData() error {
	if len(e.rcpts) == 0 {
		return SMTPError("554 5.5.1 Error: no valid recipients")
	}
	return nil
}

func (e *OGEnvelope) SetUser(user *string) {
	e.AlertUser = user
}

func (e *OGEnvelope) SetClient(alert *ogcli.OpsGenieAlertClient) {
	e.AlertClient = alert
}

//Write iterates over every line the message and checks for subject and date lines for parsing
func (e *OGEnvelope) Write(line []byte) error {
	str := string(line)

	if start := strings.HasPrefix(str, "Subject:"); start == true {
		re := regexp.MustCompile("^Subject: (.+)")
		matched := re.FindStringSubmatch(str)
		log.Printf("%q", matched)
		e.Subject = matched[1]
	}
	if start := strings.HasPrefix(str, "Date:"); start == true {
		re := regexp.MustCompile("^Date: (.+)")
		matched := re.FindStringSubmatch(str)

		dstr := matched[1]
		s := strings.Trim(dstr, " \n\r")
		dt, _ := time.Parse(time.RFC1123, s)
		e.Date = dt
	}
	return nil
}

func (e *OGEnvelope) GetHostTags() []string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Error getting hostname")
	}

	return []string{hostname}
}

func (e *OGEnvelope) GetGCETags() string {
	tagstr, err := metadata.Get("instance/tags")
	if err != nil {
		log.Printf("Tags failed to be queried: %#v\n", err)
		return ""
	}
	//TODO: Breakup tagstr into list of tags
	return tagstr
}

//Final function called for an envelope
//
//Used to send alert to OpsGenie
func (e *OGEnvelope) Close() error {

	//Data found in envelope
	str := strings.Join(e.MsgLines, "")

	dtstr := e.Date.Format(time.RFC3339)
	uptime := runCmd("uptime")
	dfh := runCmd("df -h")
	joinList := []string{str, dtstr, uptime, dfh}
	note := strings.Join(joinList, "\n")

	fmt.Println(note)

	//Send alert to OpsGenie
	req := alerts.CreateAlertRequest{Message: e.Subject, Note: note, User: *e.AlertUser}
	//req := alerts.CreateAlertRequest{Message: e.Subject, Note: note, User: *e.AlertUser, Recipients: []string{*e.AlertUser}}
	//log.Printf("->%#v<-", req)
	response, alertErr := e.AlertClient.Create(req)
	if alertErr != nil {
		log.Printf("%v", alertErr)
	} else {
		fmt.Println("alert id:", response.AlertId)
	}

	return nil
}

func runCmd(cmd string) string {
	out, err := exec.Command("/bin/bash", "-c", cmd).Output()
	if err != nil {
		fmt.Printf("Command failed: %#v\n", err)
		return fmt.Sprintf("CMD %s run failed", cmd)
	}
	return string(out)
}

type ogsession struct {
	srv *OGServer
	rwc net.Conn
	br  *bufio.Reader
	bw  *bufio.Writer

	env OGEnvelopeInterface // current envelope, or nil

	helloType string
	helloHost string
}

func (s *ogsession) errorf(format string, args ...interface{}) {
	log.Printf("Client error: "+format, args...)
}

func (s *ogsession) sendf(format string, args ...interface{}) {
	if s.srv.WriteTimeout != 0 {
		s.rwc.SetWriteDeadline(time.Now().Add(s.srv.WriteTimeout))
	}
	fmt.Fprintf(s.bw, format, args...)
	s.bw.Flush()
}

func (s *ogsession) sendlinef(format string, args ...interface{}) {
	s.sendf(format+"\r\n", args...)
}

func (s *ogsession) sendSMTPErrorOrLinef(err error, format string, args ...interface{}) {
	if se, ok := err.(SMTPError); ok {
		s.sendlinef("%s", se.Error())
		return
	}
	s.sendlinef(format, args...)
}

func (s *ogsession) Addr() net.Addr {
	return s.rwc.RemoteAddr()
}

func (s *ogsession) serve() {
	defer s.rwc.Close()
	if onc := s.srv.OnNewConnection; onc != nil {
		if err := onc(s); err != nil {
			s.sendSMTPErrorOrLinef(err, "554 connection rejected")
			return
		}
	}
	s.sendf("220 %s ESMTP gosmtpd\r\n", s.srv.hostname())
	for {
		if s.srv.ReadTimeout != 0 {
			s.rwc.SetReadDeadline(time.Now().Add(s.srv.ReadTimeout))
		}
		sl, err := s.br.ReadSlice('\n')
		if err != nil {
			s.errorf("read error: %v", err)
			return
		}
		line := cmdLine(string(sl))
		if err := line.checkValid(); err != nil {
			s.sendlinef("500 %v", err)
			continue
		}

		switch line.Verb() {
		case "HELO", "EHLO":
			s.handleHello(line.Verb(), line.Arg())
		case "QUIT":
			s.sendlinef("221 2.0.0 Bye")
			return
		case "RSET":
			s.env = nil
			s.sendlinef("250 2.0.0 OK")
		case "NOOP":
			s.sendlinef("250 2.0.0 OK")
		case "MAIL":
			arg := line.Arg() // "From:<foo@bar.com>"
			m := mailFromRE.FindStringSubmatch(arg)
			if m == nil {
				log.Printf("invalid MAIL arg: %q", arg)
				s.sendlinef("501 5.1.7 Bad sender address syntax")
				continue
			}
			s.handleMailFrom(m[1])
		case "RCPT":
			s.handleRcpt(line)
		case "DATA":
			s.handleData()
		default:
			log.Printf("Client: %q, verhb: %q", line, line.Verb())
			s.sendlinef("502 5.5.2 Error: command not recognized")
		}
	}
}

func (s *ogsession) handleHello(greeting, host string) {
	s.helloType = greeting
	s.helloHost = host
	fmt.Fprintf(s.bw, "250-%s\r\n", s.srv.hostname())
	extensions := []string{}
	if s.srv.PlainAuth {
		extensions = append(extensions, "250-AUTH PLAIN")
	}
	extensions = append(extensions, "250-PIPELINING",
		"250-SIZE 10240000",
		"250-ENHANCEDSTATUSCODES",
		"250-8BITMIME",
		"250 DSN")
	for _, ext := range extensions {
		fmt.Fprintf(s.bw, "%s\r\n", ext)
	}
	s.bw.Flush()
}

func (s *ogsession) handleMailFrom(email string) {
	// TODO: 4.1.1.11.  If the server SMTP does not recognize or
	// cannot implement one or more of the parameters associated
	// qwith a particular MAIL FROM or RCPT TO command, it will return
	// code 555.

	if s.env != nil {
		s.sendlinef("503 5.5.1 Error: nested MAIL command")
		return
	}
	log.Printf("mail from: %q", email)
	cb := s.srv.OnNewMail
	if cb == nil {
		log.Printf("smtp: Server.OnNewMail is nil; rejecting MAIL FROM")
		s.sendf("451 Server.OnNewMail not configured\r\n")
		return
	}
	s.env = nil
	env, err := cb(s, addrString(email))
	if err != nil {
		log.Printf("rejecting MAIL FROM %q: %v", email, err)
		s.sendf("451 denied\r\n")

		s.bw.Flush()
		time.Sleep(100 * time.Millisecond)
		s.rwc.Close()
		return
	}
	s.env = env
	s.sendlinef("250 2.1.0 Ok")
}

func (s *ogsession) handleRcpt(line cmdLine) {
	// TODO: 4.1.1.11.  If the server SMTP does not recognize or
	// cannot implement one or more of the parameters associated
	// qwith a particular MAIL FROM or RCPT TO command, it will return
	// code 555.

	if s.env == nil {
		s.sendlinef("503 5.5.1 Error: need MAIL command")
		return
	}
	arg := line.Arg() // "To:<foo@bar.com>"
	m := rcptToRE.FindStringSubmatch(arg)
	if m == nil {
		log.Printf("bad RCPT address: %q", arg)
		s.sendlinef("501 5.1.7 Bad sender address syntax")
		return
	}
	err := s.env.AddRecipient(addrString(m[1]))
	if err != nil {
		s.sendSMTPErrorOrLinef(err, "550 bad recipient")
		return
	}
	s.sendlinef("250 2.1.0 Ok")
}

func (s *ogsession) handleData() {
	if s.env == nil {
		s.sendlinef("503 5.5.1 Error: need RCPT command")
		return
	}
	if err := s.env.BeginData(); err != nil {
		s.handleError(err)
		return
	}
	s.sendlinef("354 Go ahead")
	for {
		sl, err := s.br.ReadSlice('\n')
		if err != nil {
			s.errorf("read error: %v", err)
			return
		}
		if bytes.Equal(sl, []byte(".\r\n")) {
			break
		}
		if sl[0] == '.' {
			sl = sl[1:]
		}
		err = s.env.Write(sl)
		if err != nil {
			s.sendSMTPErrorOrLinef(err, "550 ??? failed")
			return
		}
	}
	s.env.Close()
	s.sendlinef("250 2.0.0 Ok: queued")
	s.env = nil
}

func (s *ogsession) handleError(err error) {
	if se, ok := err.(SMTPError); ok {
		s.sendlinef("%s", se)
		return
	}
	log.Printf("Error: %s", err)
	s.env = nil
}
