package service

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

/*
Outbound mail over SMTP, with the standard library and nothing else.

The only thing that sends mail here is the password-reset link, at a volume of a
few messages an hour. A transactional-email SDK would be several megabytes of
dependency for one Write call, so this is net/smtp against a relay you
authenticate to — in practice the Student Union's Google Workspace account,
whose daily cap (2000 for Workspace, 500 for a plain Gmail) is orders of
magnitude above what resets need.

Relaying through Workspace rather than delivering to MX directly is not laziness
about configuration. Every recipient address this server will ever use ends in
@lamduan.mfu.ac.th or @mfu.ac.th, which are Google-hosted, and mail arriving
straight from a self-hosted box on a consumer IP with no SPF alignment is
filtered as spam or refused outright. A reset link that lands in spam is the
same as no reset link, except that the student cannot tell.

No SMTP_HOST configured leaves this disabled and every Send a no-op, the same
shape as WBWPushService with no service account: the rest of the server runs
untouched, which is what you want on a dev machine that has no relay to talk to.
*/

const (
	// Whole conversation, dial through QUIT. Generous, because it is spent on a
	// detached goroutine and never on a request — see WBWAuthService.RequestPasswordReset.
	mailTimeout     = 30 * time.Second
	mailDialTimeout = 10 * time.Second
)

type MailService struct {
	addr string // host:port
	host string // bare host, for SNI and the AUTH exchange
	auth smtp.Auth
	from string // envelope sender, a bare address
	// Display name plus address, for the From: header. Empty when no
	// SMTP_FROM_NAME is set, in which case the bare address is used.
	fromHeader string
	// devLog prints the message body to the log INSTEAD of sending, when mail
	// is unconfigured outside production. It is how the reset flow is exercised
	// on a dev machine; it would be a way to read other people's reset links out
	// of a log file, so it stays off whenever ENV=production.
	devLog bool

	sent   atomic.Int64
	failed atomic.Int64
}

// NewMailService reads SMTP_* from the environment. An unset SMTP_HOST is not an
// error: it returns a service whose Send is a no-op (or a log line, in dev).
func NewMailService() *MailService {
	s := &MailService{}

	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	if host == "" {
		s.devLog = os.Getenv("ENV") != "production"
		if s.devLog {
			slog.Warn("อีเมลปิดอยู่: ไม่ได้ตั้ง SMTP_HOST — ลิงก์รีเซ็ตรหัสผ่านจะถูกพิมพ์ลง log แทนการส่งจริง")
		} else {
			slog.Error("อีเมลปิดอยู่: ไม่ได้ตั้ง SMTP_HOST — ไม่มีใครรีเซ็ตรหัสผ่านเองได้")
		}
		return s
	}

	port := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	if port == "" {
		port = "587"
	}
	user := os.Getenv("SMTP_USER")
	pass := os.Getenv("SMTP_PASS")

	from := strings.TrimSpace(os.Getenv("SMTP_FROM"))
	if from == "" {
		from = user
	}
	if from == "" {
		slog.Error("อีเมลปิดอยู่: ตั้ง SMTP_HOST แล้วแต่ไม่มี SMTP_FROM หรือ SMTP_USER")
		return s
	}

	s.host = host
	s.addr = net.JoinHostPort(host, port)
	s.from = from
	s.fromHeader = from
	if name := strings.TrimSpace(os.Getenv("SMTP_FROM_NAME")); name != "" {
		// Encoded, because the name is Thai in practice and a raw UTF-8 header
		// is not legal on the wire.
		s.fromHeader = fmt.Sprintf("%s <%s>", mime.BEncoding.Encode("UTF-8", name), from)
	}
	if user != "" {
		s.auth = smtp.PlainAuth("", user, pass, host)
	}
	return s
}

func (s *MailService) Enabled() bool { return s.addr != "" }

// Stats reports what has been attempted, for the stats page and for answering
// "did the mail actually go out" without grepping logs.
func (s *MailService) Stats() (sent, failed int64) {
	return s.sent.Load(), s.failed.Load()
}

// Send delivers one plain-text UTF-8 message. It blocks for up to mailTimeout,
// so call it from a goroutine, not from a handler.
func (s *MailService) Send(to, subject, body string) error {
	if !s.Enabled() {
		if s.devLog {
			slog.Warn("อีเมล (dev, ไม่ได้ส่งจริง)", "to", to, "subject", subject, "body", body)
		}
		return nil
	}

	msg := s.compose(to, subject, body)

	// Not smtp.SendMail: it dials with no timeout and sets no deadline, so a
	// relay that accepts the connection and then says nothing parks this
	// goroutine forever. Nothing upstream can cancel it — the request that
	// triggered the send returned long ago.
	conn, err := net.DialTimeout("tcp", s.addr, mailDialTimeout)
	if err != nil {
		s.failed.Add(1)
		return fmt.Errorf("smtp dial: %w", err)
	}
	if err := conn.SetDeadline(time.Now().Add(mailTimeout)); err != nil {
		conn.Close()
		s.failed.Add(1)
		return fmt.Errorf("smtp deadline: %w", err)
	}

	if err := s.deliver(conn, to, msg); err != nil {
		conn.Close()
		s.failed.Add(1)
		return err
	}
	s.sent.Add(1)
	return nil
}

// deliver runs the SMTP conversation on an already-dialled connection. It owns
// the *smtp.Client but not the net.Conn: Send closes that on every path, so a
// failure partway through the exchange cannot leak the socket.
func (s *MailService) deliver(conn net.Conn, to string, msg []byte) error {
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()

	// STARTTLS before AUTH, always. PlainAuth refuses to hand a password to an
	// unencrypted connection anyway, but the recipient address and the reset
	// link would still cross the wire in the clear.
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.host}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	} else if s.auth != nil {
		return fmt.Errorf("smtp: %s ไม่รองรับ STARTTLS แต่มี SMTP_USER ตั้งไว้ — ไม่ส่งรหัสผ่านผ่านช่องที่ไม่เข้ารหัส", s.host)
	}

	if s.auth != nil {
		if err := c.Auth(s.auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(s.from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	// The server's acceptance comes back from this Close, not from Write — an
	// error here means the message was NOT queued, however well the writes went.
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return c.Quit()
}

// compose builds an RFC 5322 message. Subject and body are Thai, so neither can
// travel as raw bytes: the subject is B-encoded per RFC 2047 and the body is
// declared UTF-8 and base64'd, which also sidesteps the 998-octet line limit.
func (s *MailService) compose(to, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", s.fromHeader)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.BEncoding.Encode("UTF-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	// Reset mail is a reply to something the recipient did seconds ago; an
	// auto-responder answering it helps nobody and can bounce back into the
	// relay's rate limit.
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	b.WriteString(encodeBase64Lines(body))
	return []byte(b.String())
}

// encodeBase64Lines wraps base64 at 76 columns with CRLF. RFC 2045 caps an
// encoded line there, and relays are entitled to reject or rewrap a longer one —
// rewrapping a body that is already one enormous line is how a message arrives
// with its text mangled.
func encodeBase64Lines(s string) string {
	const width = 76
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	var b strings.Builder
	for i := 0; i < len(enc); i += width {
		end := min(i+width, len(enc))
		b.WriteString(enc[i:end])
		b.WriteString("\r\n")
	}
	return b.String()
}
