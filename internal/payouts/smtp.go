package payouts

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type SMTPConfig struct {
	Address  string
	Host     string
	Username string
	Password string
	From     string
	Timeout  time.Duration
}

type SMTPSender struct {
	config SMTPConfig
}

func NewSMTPSender(config SMTPConfig) (*SMTPSender, error) {
	if config.Address == "" || config.Host == "" || config.From == "" {
		return nil, errors.New("SMTP address, host, and sender are required")
	}
	from, err := mail.ParseAddress(config.From)
	if err != nil || from.Address != config.From || strings.ContainsAny(config.From, "\r\n") {
		return nil, errors.New("invalid SMTP sender address")
	}
	if (config.Username == "") != (config.Password == "") {
		return nil, errors.New("SMTP username and password must be configured together")
	}
	if config.Timeout <= 0 {
		config.Timeout = 15 * time.Second
	}
	return &SMTPSender{config: config}, nil
}

func (s *SMTPSender) Send(ctx context.Context, message Message) (string, error) {
	recipient, err := mail.ParseAddress(message.Recipient)
	if err != nil || recipient.Address != message.Recipient ||
		strings.ContainsAny(message.Recipient, "\r\n") {
		return "", errors.New("invalid payout recipient address")
	}
	from, err := mail.ParseAddress(s.config.From)
	if err != nil {
		return "", errors.New("invalid configured sender address")
	}
	domain := from.Address[strings.LastIndexByte(from.Address, '@')+1:]
	messageID := fmt.Sprintf("<weekly-payout-%s@%s>", message.ID, domain)

	dialer := net.Dialer{Timeout: s.config.Timeout}
	connection, err := dialer.DialContext(ctx, "tcp", s.config.Address)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	deadline := time.Now().Add(s.config.Timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return "", err
	}
	client, err := smtp.NewClient(connection, s.config.Host)
	if err != nil {
		return "", err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return "", errors.New("SMTP server does not support required STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{
		ServerName: s.config.Host,
		MinVersion: tls.VersionTLS12,
	}); err != nil {
		return "", err
	}
	if s.config.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return "", errors.New("SMTP server does not advertise AUTH")
		}
		auth := smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)
		if err := client.Auth(auth); err != nil {
			return "", err
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return "", err
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return "", err
	}
	writer, err := client.Data()
	if err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(writer, "%s\r\n", payoutEmail(
		from.Address, recipient.Address, messageID, message.Statement,
	)); err != nil {
		_ = writer.Close()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	if err := client.Quit(); err != nil {
		return "", err
	}
	return messageID, nil
}

func payoutEmail(from, to, messageID string, statement WeeklyStatement) string {
	var body strings.Builder
	fmt.Fprintf(&body, "From: %s\r\n", from)
	fmt.Fprintf(&body, "To: %s\r\n", to)
	body.WriteString("Subject: Your ShopStops weekly courier earnings statement\r\n")
	fmt.Fprintf(&body, "Message-ID: %s\r\n", messageID)
	body.WriteString("MIME-Version: 1.0\r\n")
	body.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	body.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	fmt.Fprintf(&body, "Weekly courier earnings statement for %s\n\n",
		statement.PeriodStart.Format("Jan 2, 2006"))
	fmt.Fprintf(&body, "Period: %s through %s (UTC)\n",
		statement.PeriodStart.Format("Jan 2, 2006"),
		statement.PeriodEnd.Add(-24*time.Hour).Format("Jan 2, 2006"))
	fmt.Fprintf(&body, "Completed routes: %d\n", statement.CompletedRoutes)
	fmt.Fprintf(&body, "Distance: %.2f miles\n", float64(statement.DistanceMeters)/1609.344)
	fmt.Fprintf(&body, "Base pay: %s\n", dollars(statement.BasePayCents))
	fmt.Fprintf(&body, "Mileage pay: %s\n", dollars(statement.MileagePayCents))
	fmt.Fprintf(&body, "Total earnings: %s\n\n", dollars(statement.TotalEarningsCents))
	body.WriteString("This is an earnings statement, not a confirmation of funds transfer.\n")
	body.WriteString("Contact ShopStops support if you believe these figures are incorrect.\n")
	return body.String()
}

func dollars(cents int64) string {
	return "$" + strconv.FormatInt(cents/100, 10) + "." +
		fmt.Sprintf("%02d", cents%100)
}
