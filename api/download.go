package api

import (
	"fmt"
	"io"
	"log"
	"mime"
	"strconv"

	"backend-go/tgclient"

	"github.com/gofiber/fiber/v2"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

func SetupDownloadRoutes(app *fiber.App) {
	app.Get("/api/file/:message_id", HandleGetFile)
	app.Get("/api/thumbnail/:message_id", HandleGetThumbnail)
}

func HandleGetFile(c *fiber.Ctx) error {
	messageIDStr := c.Params("message_id")
	messageID, err := strconv.Atoi(messageIDStr)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"detail": "Invalid message_id"})
	}

	tgc := c.Locals("tg").(*tgclient.TGClient)
	ctx := c.Context()

	// Direct fetch since .Context() isn't working as expected
	msgs, err := tgc.API.MessagesGetMessages(ctx, []tg.InputMessageClass{
		&tg.InputMessageID{ID: messageID},
	})
	if err != nil {
		log.Printf("Failed to get message %d: %v", messageID, err)
		return c.Status(404).JSON(fiber.Map{"detail": "File not found"})
	}

	var foundMsg *tg.Message
	if msgBox, ok := msgs.(*tg.MessagesMessages); ok {
		for _, m := range msgBox.Messages {
			if msg, ok := m.(*tg.Message); ok && msg.ID == messageID {
				foundMsg = msg
				break
			}
		}
	} else if msgSlice, ok := msgs.(*tg.MessagesChannelMessages); ok {
		for _, m := range msgSlice.Messages {
			if msg, ok := m.(*tg.Message); ok && msg.ID == messageID {
				foundMsg = msg
				break
			}
		}
	}

	if foundMsg == nil || foundMsg.Media == nil {
		return c.Status(404).JSON(fiber.Map{"detail": "File not found"})
	}

	doc, ok := foundMsg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return c.Status(404).JSON(fiber.Map{"detail": "File not found"})
	}

	d, ok := doc.Document.(*tg.Document)
	if !ok {
		return c.Status(404).JSON(fiber.Map{"detail": "File not found"})
	}

	fileSize := d.Size
	mimeType := d.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	fileName := "file_" + messageIDStr
	for _, attr := range d.Attributes {
		if fnAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
			fileName = fnAttr.FileName
			break
		}
	}

	if fileName == "file_"+messageIDStr {
		exts, _ := mime.ExtensionsByType(mimeType)
		if len(exts) > 0 {
			fileName += exts[0]
		}
	}

	// NOTE: Streaming the document efficiently with Fiber is usually done
	// by passing an io.Reader to c.SendStream. Since `gotd` document downloading
	// expects an io.Writer (like an *os.File), we'll create an in-memory Pipe.

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		downloader := downloader.NewDownloader()
		fileLoc := &tg.InputDocumentFileLocation{
			ID:            d.ID,
			AccessHash:    d.AccessHash,
			FileReference: d.FileReference,
		}

		_, err := downloader.Download(tgc.API, fileLoc).Stream(ctx, pw)
		if err != nil {
			log.Printf("Download error for message %d: %v", messageID, err)
		}
	}()

	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	c.Set("Content-Length", strconv.FormatInt(fileSize, 10))
	c.Set("Accept-Ranges", "bytes")
	c.Set("Cache-Control", "public, max-age=31536000")
	c.Set("Content-Type", "application/octet-stream")

	return c.SendStream(pr)
}

func HandleGetThumbnail(c *fiber.Ctx) error {
	messageIDStr := c.Params("message_id")
	messageID, err := strconv.Atoi(messageIDStr)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"detail": "Invalid message_id"})
	}

	tgc := c.Locals("tg").(*tgclient.TGClient)
	ctx := c.Context()

	// Direct fetch
	msgs, err := tgc.API.MessagesGetMessages(ctx, []tg.InputMessageClass{
		&tg.InputMessageID{ID: messageID},
	})
	if err != nil {
		log.Printf("Failed to get thumbnail msg %d: %v", messageID, err)
		return c.Status(404).JSON(fiber.Map{"detail": "File not found"})
	}

	var foundMsg *tg.Message
	if msgBox, ok := msgs.(*tg.MessagesMessages); ok {
		for _, m := range msgBox.Messages {
			if msg, ok := m.(*tg.Message); ok && msg.ID == messageID {
				foundMsg = msg
				break
			}
		}
	} else if msgSlice, ok := msgs.(*tg.MessagesChannelMessages); ok {
		for _, m := range msgSlice.Messages {
			if msg, ok := m.(*tg.Message); ok && msg.ID == messageID {
				foundMsg = msg
				break
			}
		}
	}

	if foundMsg == nil || foundMsg.Media == nil {
		return c.Status(404).JSON(fiber.Map{"detail": "File not found"})
	}

	doc, ok := foundMsg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return c.Status(404).JSON(fiber.Map{"detail": "File not found"})
	}

	d, ok := doc.Document.(*tg.Document)
	if !ok {
		return c.Status(404).JSON(fiber.Map{"detail": "File not found"})
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		downloader := downloader.NewDownloader()
		fileLoc := &tg.InputDocumentFileLocation{
			ID:            d.ID,
			AccessHash:    d.AccessHash,
			FileReference: d.FileReference,
		}

		_, err := downloader.Download(tgc.API, fileLoc).Stream(ctx, pw)
		if err != nil {
			log.Printf("Download error for thumbnail %d: %v", messageID, err)
		}
	}()

	c.Set("Cache-Control", "public, max-age=31536000")
	c.Set("Content-Type", "application/octet-stream")

	return c.SendStream(pr)
}
