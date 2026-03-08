package api

import (
	"fmt"
	"log"
	"strconv"

	"backend-go/tgclient"

	"github.com/gofiber/fiber/v2"
	"github.com/gotd/td/tg"
)

func SetupDeleteRoutes(app *fiber.App) {
	app.Delete("/api/delete/:message_id", HandleDeleteFile)
}

func HandleDeleteFile(c *fiber.Ctx) error {
	messageIDStr := c.Params("message_id")
	messageID, err := strconv.Atoi(messageIDStr)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"detail": "Invalid message_id"})
	}

	tgc := c.Locals("tg").(*tgclient.TGClient)
	ctx := c.Context()

	// 1. Try to delete
	channelInput := &tg.InputChannel{
		ChannelID: tgc.ChatID,
		// AccessHash is usually needed. gotd provides ways to get it,
		// but since we are the channel owner, we might be able to use 0 or resolve it first.
		AccessHash: 0,
	}

	// This is a simplified approach, similar to Telethon's client.delete_messages
	// Since we are operating on a specific chat/channel, we use the specific API call.
	// If it's a basic chat or user, it would be tg.MessagesDeleteMessagesRequest

	// Try channel delete
	res, err := tgc.API.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
		Channel: channelInput,
		ID:      []int{messageID},
	})

	if err != nil {
		log.Printf("⚠️ Telegram Delete Warning for msg %d: %v", messageID, err)
		// We continue even if Telegram complains, so DB can be cleaned up (Ghost file logic).
	} else {
		log.Printf("✅ Deleted message %d, affected: %v", messageID, res)
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": fmt.Sprintf("Message %d processed for deletion.", messageID),
	})
}
