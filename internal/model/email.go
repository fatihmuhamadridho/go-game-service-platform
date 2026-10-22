package model

type EmailSender interface {
	Configured() bool
	Send(to, subject, textBody, htmlBody string) error
}
