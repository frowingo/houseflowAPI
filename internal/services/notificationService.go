package services

import (
	"bytes"
	"fmt"
	"houseflowApi/internal/config"
	"net/smtp"
	"strings"
)

const MailSMTPHost = "smtp.gmail.com"
const MailSMTPPort = 587
const MailSMTPUsername = "houseflow37@gmail.com"
const MailFromAddress = "no-reply@houseflow.com"

type NotificationService struct{}

func NewNotificationService() *NotificationService {
	return &NotificationService{}
}

func (r *NotificationService) SendResetCodeEmail(toEmail, code string, validityMinutes int) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	smtpPassword := cfg.Internal.SMTP.Password
	if smtpPassword == "" {
		return fmt.Errorf("smtp password is missing")
	}

	smtpAddress := fmt.Sprintf("%s:%d", MailSMTPHost, MailSMTPPort)
	auth := smtp.PlainAuth("", MailSMTPUsername, smtpPassword, MailSMTPHost)

	subject := "Password Reset Code"
	codeChars := strings.Split(code, "")
	var codeCells bytes.Buffer
	for _, ch := range codeChars {
		codeCells.WriteString(`<td align="center" valign="middle" width="40" style="width:40px;height:52px;border-radius:10px;background:#0f766e;color:#ffffff;font-size:24px;font-weight:700;letter-spacing:2px;">`)
		codeCells.WriteString(ch)
		codeCells.WriteString(`</td>`)
	}

	htmlBody := fmt.Sprintf(`
<!doctype html>
<html>
	<body style="margin:0;padding:0;background:#e6f7f2;font-family:Arial,Helvetica,sans-serif;color:#0f172a;">
		<div style="max-width:640px;margin:0 auto;padding:24px 12px;">
			<div style="background:#ffffff;border-radius:20px;overflow:hidden;box-shadow:0 16px 48px rgba(12,74,110,0.14);border:1px solid #dbeafe;">
				<div style="padding:24px 20px;background:linear-gradient(135deg,#e0f2fe 0%%,#dcfce7 100%%);color:#0f172a;border-bottom:1px solid #bae6fd;">
					<div style="font-size:12px;letter-spacing:2px;text-transform:uppercase;color:#0c4a6e;font-weight:700;">HouseFlow</div>
					<h1 style="margin:10px 0 0;font-size:26px;line-height:1.25;color:#0f172a;">Password Reset Code</h1>
        </div>
				<div style="padding:24px 20px;">
					<p style="margin:0 0 16px;font-size:16px;line-height:1.7;color:#334155;">We received a request to reset your password. Use the code below to continue.</p>
					<table role="presentation" cellpadding="0" cellspacing="5" border="0" style="margin:22px 0 18px;border-collapse:separate;table-layout:fixed;">
						<tr>
							%s
						</tr>
					</table>
					<div style="padding:14px 16px;border-radius:14px;background:#ecfeff;border:1px solid #99f6e4;color:#0f766e;font-size:14px;line-height:1.6;">
            This code expires in <strong>%d minutes</strong>.
          </div>
					<p style="margin:20px 0 0;font-size:13px;line-height:1.7;color:#64748b;">If you did not request this password reset, you can safely ignore this email.</p>
        </div>
				<div style="padding:16px 20px;background:#f0fdfa;border-top:1px solid #ccfbf1;font-size:12px;line-height:1.6;color:#0f766e;">
          HouseFlow Security Team
        </div>
      </div>
    </div>
  </body>
</html>`, codeCells.String(), validityMinutes)
	message := []byte(
		"From: " + MailFromAddress + "\r\n" +
			"To: " + toEmail + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/html; charset=UTF-8\r\n" +
			"Content-Transfer-Encoding: 8bit\r\n\r\n" +
			htmlBody + "\r\n",
	)

	return smtp.SendMail(smtpAddress, auth, MailFromAddress, []string{toEmail}, message)
}
