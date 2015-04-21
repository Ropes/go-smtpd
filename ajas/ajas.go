package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"

	ogcli "github.com/opsgenie/opsgenie-go-sdk/client"
	"github.com/pivotal/go-smtpd/smtpd"
)

type env struct {
	*smtpd.OGEnvelope
}

var ogkey *string
var ogaccnt *string
var alertCli *ogcli.OpsGenieAlertClient

func (e *env) AddRecipient(rcpt smtpd.MailAddress) error {
	if strings.HasPrefix(rcpt.Email(), "bad@") {
		return errors.New("we don't send email to bad@")
	}
	return e.OGEnvelope.AddRecipient(rcpt)
}

func onNewMail(c smtpd.Connection, from smtpd.MailAddress) (smtpd.Envelope, error) {
	log.Printf("ajas: new mail from %q", from)
	log.Printf("OG alertCli %#v", alertCli)
	lope := new(smtpd.OGEnvelope)
	//lope := smtpd.OGEnvelope{AlertUser: ogaccnt}
	//lope.AlertUser = ogaccnt
	lope.SetClient(alertCli)
	lope.SetUser(ogaccnt)
	log.Printf("OG Lope\n%#v", lope)
	return &env{lope}, nil
}

func main() {
	ogkey = flag.String("ogkey", "YOURKEY", "OpsGenie API key for creating Alerts")
	ogaccnt = flag.String("ogaccnt", "USER", "OpsGenie account which the client will target")

	flag.Parse()

	cli := new(ogcli.OpsGenieClient)
	cli.SetApiKey(*ogkey)
	fmt.Printf("%#v\n", cli)

	aCli, cliErr := cli.Alert()
	if cliErr != nil {
		panic(cliErr)
	}
	log.Printf("\nAlertCLI: %#v", aCli)
	alertCli = aCli
	fmt.Printf("%#v %s", *alertCli, *ogaccnt)

	s := &smtpd.Server{
		Addr:      ":2500",
		OnNewMail: onNewMail,
	}
	err := s.ListenAndServe()
	if err != nil {
		log.Fatalf("ListenAndServe: %v", err)
	}
}
