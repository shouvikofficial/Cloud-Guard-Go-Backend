package tgclient

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// TGClient is a wrapper around gotd/td Client
type TGClient struct {
	Client *telegram.Client
	API    *tg.Client
	ChatID int64
}

// InputPeer provides the parsed channel peer for sending
func (t *TGClient) InputPeer() tg.InputPeerClass {
	// For sending to a specific channel:
	return &tg.InputPeerChannel{
		ChannelID:  t.ChatID,
		AccessHash: 0, // 0 usually works if you're the owner, or if the API resolves it
	}
}

// NewTGClient creates a new Telegram client connected to the API
func NewTGClient(ctx context.Context) (*TGClient, error) {
	apiIDStr := os.Getenv("API_ID")
	apiHash := os.Getenv("API_HASH")
	chatIDStr := os.Getenv("CHAT_ID")

	if apiIDStr == "" || apiHash == "" || chatIDStr == "" {
		return nil, fmt.Errorf("API_ID, API_HASH, or CHAT_ID not set")
	}

	apiID, err := strconv.Atoi(apiIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid API_ID: %v", err)
	}

	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid CHAT_ID: %v", err)
	}

	sessionFile := "tg_session.json"

	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: sessionFile},
	})

	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("auth status error: %w", err)
		}

		if !status.Authorized {
			log.Println("Not authorized. Starting terminal auth flow...")
			flow := auth.NewFlow(termAuth{}, auth.SendCodeOptions{})
			if err := client.Auth().IfNecessary(ctx, flow); err != nil {
				return fmt.Errorf("auth flow error: %w", err)
			}
			log.Println("Successfully authorized!")
		} else {
			log.Println("Already authorized.")
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return &TGClient{
		Client: client,
		API:    client.API(),
		ChatID: chatID,
	}, nil
}

type termAuth struct{}

func (t termAuth) Phone(_ context.Context) (string, error) {
	fmt.Print("Enter phone number (e.g. +1234567890): ")
	var phone string
	fmt.Scanln(&phone)
	return phone, nil
}

func (t termAuth) Password(_ context.Context) (string, error) {
	fmt.Print("Enter 2FA password (if any): ")
	var pwd string
	fmt.Scanln(&pwd)
	return pwd, nil
}

func (t termAuth) AcceptTermsOfService(_ context.Context, tos tg.HelpTermsOfService) error {
	return &auth.SignUpRequired{TermsOfService: tos}
}

func (t termAuth) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign up is not supported")
}

func (t termAuth) Code(_ context.Context, _ *tg.AuthSentCode) (string, error) {
	fmt.Print("Enter Telegram code: ")
	var code string
	fmt.Scanln(&code)
	return code, nil
}
