// Package ses — AWS Simple Email Service (v2) adapter dengan mock fallback.
//
// Mode:
//   - Mock=true   : log-only, no AWS call (default untuk dev/CI)
//   - Mock=false  : panggil AWS SES v2 SendEmail API
//
// AWS credentials di-load via aws-sdk-go-v2 default credential chain:
//  1. Environment variables: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY
//  2. Shared credentials file: ~/.aws/credentials
//  3. IAM role (kalau running di EC2/ECS/Lambda)
//
// Sandbox limit: kalau SES account masih di sandbox mode, recipient email
// harus di-verify dulu di AWS Console → SES → Verified Identities.
package ses

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	sesclient "github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"go.uber.org/zap"
)

// Client — EmailSender implementation.
type Client struct {
	Region  string
	From    string
	ReplyTo string
	Mock    bool
	Logger  *zap.Logger

	// awsClient — di-init saat Mock=false. Nil saat mock.
	awsClient *sesclient.Client
}

// New — constructor. Kalau Mock=true, AWS SDK tidak di-init.
func New(ctx context.Context, region, from, replyTo string, mock bool, log *zap.Logger) (*Client, error) {
	c := &Client{
		Region:  region,
		From:    from,
		ReplyTo: replyTo,
		Mock:    mock,
		Logger:  log,
	}
	if mock {
		return c, nil
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	c.awsClient = sesclient.NewFromConfig(cfg)
	return c, nil
}

// Send — implement usecase.EmailSender port.
//
// SES v2 SendEmail with Simple content (plain text body). Untuk HTML, swap
// `Text` dengan `Html` atau pakai both.
func (c *Client) Send(ctx context.Context, toEmail, toName, subject, body string) error {
	if c.Mock {
		c.Logger.Info("MOCK SES send",
			zap.String("from", c.From),
			zap.String("to", toEmail),
			zap.String("to_name", toName),
			zap.String("subject", subject),
			zap.Int("body_bytes", len(body)),
		)
		preview := body
		if len(preview) > 200 {
			preview = preview[:200] + "...(truncated)"
		}
		c.Logger.Debug("body preview", zap.String("text", preview))
		return nil
	}

	// SES v2 SendEmail
	in := &sesclient.SendEmailInput{
		FromEmailAddress: aws.String(c.From),
		Destination: &sestypes.Destination{
			ToAddresses: []string{toEmail},
		},
		Content: &sestypes.EmailContent{
			Simple: &sestypes.Message{
				Subject: &sestypes.Content{
					Data:    aws.String(subject),
					Charset: aws.String("UTF-8"),
				},
				Body: &sestypes.Body{
					Text: &sestypes.Content{
						Data:    aws.String(body),
						Charset: aws.String("UTF-8"),
					},
				},
			},
		},
	}
	if c.ReplyTo != "" {
		in.ReplyToAddresses = []string{c.ReplyTo}
	}

	out, err := c.awsClient.SendEmail(ctx, in)
	if err != nil {
		return fmt.Errorf("ses SendEmail: %w", err)
	}
	c.Logger.Info("SES email sent",
		zap.String("to", toEmail),
		zap.String("subject", subject),
		zap.String("message_id", aws.ToString(out.MessageId)),
	)
	return nil
}
