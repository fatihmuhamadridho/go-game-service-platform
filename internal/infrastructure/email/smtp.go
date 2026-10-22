package email

import (
	"errors"
	"math"
	"os"
	"strconv"
	"strings"

	mailclient "github.com/go-mail/mail/v2"
)

type Config struct {
	Host     string
	Port     int
	From     string
	Secure   bool
	Username string
	Password string
}

type SMTPSender struct{}

func NewSMTPSender() *SMTPSender { return &SMTPSender{} }

func (s *SMTPSender) Configured() bool { return Configured() }

func (s *SMTPSender) Send(to, subject, textBody, htmlBody string) error {
	return Send(to, subject, textBody, htmlBody)
}

func ReadConfig() (Config, error) {
	c := Config{Host: strings.TrimSpace(os.Getenv("SMTP_HOST")), From: strings.TrimSpace(os.Getenv("SMTP_FROM")), Username: strings.TrimSpace(os.Getenv("SMTP_USER")), Password: os.Getenv("SMTP_PASSWORD")}
	port := 587
	if rawPort, exists := os.LookupEnv("SMTP_PORT"); exists {
		portText := strings.TrimSpace(rawPort)
		portNumber, err := strconv.ParseFloat(portText, 64)
		if len(portText) > 2 && portText[0] == '0' {
			base := 0
			switch portText[1] {
			case 'x', 'X':
				base = 16
			case 'b', 'B':
				base = 2
			case 'o', 'O':
				base = 8
			}
			if base != 0 {
				var integer uint64
				integer, err = strconv.ParseUint(portText[2:], base, 64)
				portNumber = float64(integer)
			}
		}
		if err != nil || math.IsNaN(portNumber) || math.IsInf(portNumber, 0) || math.Trunc(portNumber) != portNumber || portNumber < 1 || portNumber > 65535 {
			return Config{}, errors.New("invalid SMTP_PORT")
		}
		port = int(portNumber)
	}
	c.Port = port
	secure := strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_SECURE")))
	if secure != "" && secure != "true" && secure != "false" {
		return Config{}, errors.New("invalid SMTP_SECURE")
	}
	c.Secure = (secure == "true") || (secure == "" && port == 465)
	if c.Host == "" || c.From == "" || port < 1 || port > 65535 || (c.Username == "") != (c.Password == "") {
		return Config{}, errors.New("SMTP configuration incomplete")
	}
	return c, nil
}

func Configured() bool { _, err := ReadConfig(); return err == nil }

func Send(to, subject, textBody, htmlBody string) error {
	c, err := ReadConfig()
	if err != nil {
		return err
	}
	m := mailclient.NewMessage()
	m.SetHeader("From", c.From)
	m.SetHeader("To", to)
	m.SetHeader("Subject", subject)
	m.SetBody("text/plain", textBody)
	if htmlBody != "" {
		m.AddAlternative("text/html", htmlBody)
	}
	d := mailclient.NewDialer(c.Host, c.Port, c.Username, c.Password)
	d.SSL = c.Secure
	if err := d.DialAndSend(m); err != nil {
		return errors.New("email delivery failed")
	}
	return nil
}
