package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"

	ogcli "github.com/opsgenie/opsgenie-go-sdk/client"
	"github.com/ropes/go-smtpd/smtpd"
)

type env struct {
	*smtpd.OGEnvelope
}

var ogkey *string
var ogaccnt *string
var addr *string
var alertCli *ogcli.OpsGenieAlertClient

func (e *env) AddRecipient(rcpt smtpd.MailAddress) error {
	if strings.HasPrefix(rcpt.Email(), "bad@") {
		return errors.New("we don't send email to bad@")
	}
	return e.OGEnvelope.AddRecipient(rcpt)
}

//onNewMail Creates a new envelope struct which passes objects
func onNewMail(c smtpd.Connection, from smtpd.MailAddress) (smtpd.OGEnvelopeInterface, error) {
	log.Printf("ajas: new mail from %q", from)
	lope := new(smtpd.OGEnvelope)
	lope.SetClient(alertCli)
	lope.SetUser(ogaccnt)
	return &env{lope}, nil
}

func main() {
	ogkey = flag.String("ogkey", "YOURKEY", "OpsGenie API key for creating Alerts")
	ogaccnt = flag.String("ogaccnt", "USER", "OpsGenie account which the client will target")
	addr = flag.String("addr", ":2500", "Address to listen on. eg: `:2500`")

	flag.Parse()

	cli := new(ogcli.OpsGenieClient)
	cli.SetApiKey(*ogkey)
	fmt.Printf("%#v\n", cli)

	aCli, cliErr := cli.Alert()
	//Setting `alertCli` post cli.Alert() is necessary for some reason beyond me. Potentially file scope?
	alertCli = aCli
	if cliErr != nil {
		panic(cliErr)
	}

	s := &smtpd.OGServer{
		Addr:      *addr,
		OnNewMail: onNewMail,
	}
	err := s.ListenAndServe()
	if err != nil {
		log.Fatalf("ListenAndServe: %v", err)
	}
}
