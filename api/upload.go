package api

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"backend-go/tgclient"

	"github.com/gofiber/fiber/v2"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/uploader"
)

const maxFileSize = 2 * 1024 * 1024 * 1024 // 2GB

var (
	tempUploadDir    = "temp_uploads"
	uploadSessions   = make(map[string]*UploadSession)
	cancelledUploads = make(map[string]bool)
	activeUploads    = make(map[string]*ActiveUpload)
	sessionMutex     sync.Mutex
)

func init() {
	os.MkdirAll(tempUploadDir, os.ModePerm)
}

type UploadSession struct {
	TotalChunks int
	FileName    string
	Finalized   bool
	Received    map[int]bool
	Mutex       sync.Mutex
}

type ActiveUpload struct {
	FileName    string
	StartTime   time.Time
	TotalChunks int
}

func SetupUploadRoutes(app *fiber.App) {
	app.Post("/api/upload-chunk", HandleUploadChunk)
	app.Post("/api/upload-cancel", HandleUploadCancel)
	app.Get("/api/upload-status/:upload_id", HandleUploadStatus)
	app.Get("/api/upload-stats", HandleUploadStats)
	app.Post("/api/upload-thumbnail", HandleUploadThumbnail)
}

func splitAndFindID(s string) int {
	re := regexp.MustCompile(`ID:(\d+)`)
	matches := re.FindStringSubmatch(s)
	if len(matches) > 1 {
		id, _ := strconv.Atoi(matches[1])
		return id
	}
	return 0
}

func HandleUploadChunk(c *fiber.Ctx) error {
	uploadID := c.FormValue("upload_id")
	fileName := c.FormValue("file_name")

	chunkIndexStr := c.FormValue("chunk_index")
	totalChunksStr := c.FormValue("total_chunks")

	chunkIndex, err := strconv.Atoi(chunkIndexStr)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"detail": "Invalid chunk_index"})
	}

	totalChunks, err := strconv.Atoi(totalChunksStr)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"detail": "Invalid total_chunks"})
	}

	sessionMutex.Lock()
	if cancelledUploads[uploadID] {
		sessionMutex.Unlock()
		return c.Status(499).JSON(fiber.Map{"detail": "Upload cancelled"})
	}

	if _, exists := activeUploads[uploadID]; !exists {
		activeUploads[uploadID] = &ActiveUpload{
			FileName:    fileName,
			StartTime:   time.Now(),
			TotalChunks: totalChunks,
		}
		log.Printf("📁 New upload started: %s (ID: %s, %d chunks)\n", fileName, uploadID, totalChunks)
	}

	sessionKey := "upload:" + uploadID
	session, exists := uploadSessions[sessionKey]
	if !exists {
		session = &UploadSession{
			TotalChunks: totalChunks,
			FileName:    fileName,
			Finalized:   false,
			Received:    make(map[int]bool),
		}
		uploadSessions[sessionKey] = session
		log.Printf("🆕 Created new upload session for %s\n", uploadID)
	}
	sessionMutex.Unlock()

	session.Mutex.Lock()
	if session.Finalized {
		session.Mutex.Unlock()
		return c.JSON(fiber.Map{"status": "ignored", "reason": "already_finalized"})
	}

	sessionDir := filepath.Join(tempUploadDir, uploadID)
	os.MkdirAll(sessionDir, os.ModePerm)
	chunkPath := filepath.Join(sessionDir, fmt.Sprintf("chunk_%d", chunkIndex))

	// Save chunk to disk
	file, err := c.FormFile("file")
	if err != nil {
		session.Mutex.Unlock()
		return c.Status(400).JSON(fiber.Map{"detail": "Missing file form data"})
	}

	if _, err := os.Stat(chunkPath); os.IsNotExist(err) {
		if err := c.SaveFile(file, chunkPath); err != nil {
			log.Printf("❌ Chunk save failed for %s: %v\n", uploadID, err)
			session.Mutex.Unlock()
			return c.Status(500).JSON(fiber.Map{"detail": "Chunk save failed"})
		}
	}

	session.Received[chunkIndex] = true
	receivedCount := len(session.Received)
	session.Mutex.Unlock()

	log.Printf("📊 Progress: %d/%d chunks received for %s\n", receivedCount, totalChunks, fileName)

	// Check if all chunks received
	if receivedCount == totalChunks {
		session.Mutex.Lock()
		if !session.Finalized {
			session.Finalized = true
			session.Mutex.Unlock()

			log.Printf("✅ All chunks received for %s, starting finalization...\n", fileName)
			result, err := finalizeUpload(c, sessionDir, uploadID, fileName, totalChunks)
			if err != nil {
				session.Mutex.Lock()
				session.Finalized = false
				session.Mutex.Unlock()
				log.Printf("❌ Finalization failed: %v", err)
				return c.Status(500).JSON(fiber.Map{"detail": err.Error()})
			}
			return c.JSON(result)
		}
		session.Mutex.Unlock()
	}

	return c.JSON(fiber.Map{
		"status":      "chunk_received",
		"uploaded":    receivedCount,
		"total":       totalChunks,
		"chunk_index": chunkIndex,
	})
}

func finalizeUpload(c *fiber.Ctx, sessionDir, uploadID, fileName string, totalChunks int) (fiber.Map, error) {
	finalFilePath := filepath.Join(tempUploadDir, fmt.Sprintf("%s_%s", uploadID, fileName))
	out, err := os.Create(finalFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create final file: %v", err)
	}

	log.Printf("🔨 Assembling %d chunks for %s\n", totalChunks, fileName)
	for i := 0; i < totalChunks; i++ {
		chunkPath := filepath.Join(sessionDir, fmt.Sprintf("chunk_%d", i))
		chunkFile, err := os.Open(chunkPath)
		if err != nil {
			out.Close()
			return nil, fmt.Errorf("missing chunk %d", i)
		}
		io.Copy(out, chunkFile)
		chunkFile.Close()
	}
	out.Close()

	fileStat, err := os.Stat(finalFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat final file: %v", err)
	}

	fileSize := fileStat.Size()
	log.Printf("✅ File assembled: %s (%d bytes)\n", fileName, fileSize)

	if fileSize > maxFileSize {
		os.Remove(finalFilePath)
		return nil, fmt.Errorf("file too large")
	}

	// Upload to Telegram
	tg := c.Locals("tg").(*tgclient.TGClient)
	ctx := c.Context()

	log.Printf("📤 Uploading to Telegram: %s\n", fileName)

	// Create gotd uploader
	up := uploader.NewUploader(tg.API)

	tgFile, err := up.FromPath(ctx, finalFilePath)
	if err != nil {
		return nil, fmt.Errorf("telegram upload failed: %v", err)
	}

	// Send document message
	tgc := c.Locals("tg").(*tgclient.TGClient)
	ctx = c.Context()

	sender := message.NewSender(tgc.API).To(tgc.InputPeer())

	sentMessage, err := sender.Media(ctx, message.UploadedDocument(tgFile).
		MIME("application/octet-stream").
		Filename(fileName))

	if err != nil {
		return nil, fmt.Errorf("telegram send message failed: %v", err)
	}

	// Cleanup
	go func() {
		os.Remove(finalFilePath)
		os.RemoveAll(sessionDir)

		sessionMutex.Lock()
		delete(uploadSessions, "upload:"+uploadID)
		delete(activeUploads, uploadID)
		delete(cancelledUploads, uploadID)
		sessionMutex.Unlock()
	}()

	var messageID int

	msgStr := fmt.Sprintf("%v", sentMessage)
	importStrParts := splitAndFindID(msgStr)
	if importStrParts > 0 {
		messageID = importStrParts
	}

	return fiber.Map{
		"status":     "done",
		"message_id": messageID,
		"file_id":    fmt.Sprintf("%d", messageID),
		"type":       "application/octet-stream",
		"file_name":  fileName,
		"size":       fileSize,
	}, nil
}

func HandleUploadCancel(c *fiber.Ctx) error {
	type CancelReq struct {
		UploadID string `json:"upload_id"`
	}
	var req CancelReq
	if err := c.BodyParser(&req); err != nil || req.UploadID == "" {
		return c.Status(400).JSON(fiber.Map{"detail": "Missing upload_id"})
	}

	sessionMutex.Lock()
	cancelledUploads[req.UploadID] = true
	sessionKey := "upload:" + req.UploadID
	delete(uploadSessions, sessionKey)
	delete(activeUploads, req.UploadID)
	sessionMutex.Unlock()

	sessionDir := filepath.Join(tempUploadDir, req.UploadID)
	os.RemoveAll(sessionDir)

	return c.JSON(fiber.Map{"status": "cancelled"})
}

func HandleUploadStatus(c *fiber.Ctx) error {
	uploadID := c.Params("upload_id")
	sessionKey := "upload:" + uploadID

	sessionMutex.Lock()
	session, exists := uploadSessions[sessionKey]
	sessionMutex.Unlock()

	if !exists {
		return c.JSON(fiber.Map{"status": "not_found"})
	}

	session.Mutex.Lock()
	uploaded := len(session.Received)
	total := session.TotalChunks
	finalized := session.Finalized
	session.Mutex.Unlock()

	status := "uploading"
	if finalized {
		status = "finalizing"
	}

	return c.JSON(fiber.Map{
		"status":    status,
		"uploaded":  uploaded,
		"total":     total,
		"finalized": finalized,
	})
}

func HandleUploadStats(c *fiber.Ctx) error {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()

	var uploads []fiber.Map
	for uid, info := range activeUploads {
		uploads = append(uploads, fiber.Map{
			"upload_id":       uid,
			"file_name":       info.FileName,
			"total_chunks":    info.TotalChunks,
			"elapsed_seconds": time.Since(info.StartTime).Seconds(),
		})
	}

	return c.JSON(fiber.Map{
		"active_uploads":  len(activeUploads),
		"active_sessions": len(uploadSessions),
		"uploads":         uploads,
	})
}

func HandleUploadThumbnail(c *fiber.Ctx) error {
	uploadID := c.FormValue("upload_id")
	file, err := c.FormFile("file")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"detail": "Missing file thumbnail"})
	}

	suffix := uploadID
	if suffix == "" {
		suffix = file.Filename
	}
	tempThumbPath := filepath.Join(tempUploadDir, fmt.Sprintf("thumb_%s.enc", suffix))

	if err := c.SaveFile(file, tempThumbPath); err != nil {
		return c.Status(500).JSON(fiber.Map{"detail": "Failed to save thumbnail"})
	}
	defer os.Remove(tempThumbPath)

	tg := c.Locals("tg").(*tgclient.TGClient)
	ctx := c.Context()

	up := uploader.NewUploader(tg.API)
	tgFile, err := up.FromPath(ctx, tempThumbPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"detail": "Telegram thumb upload failed"})
	}

	sender := message.NewSender(tg.API).To(tg.InputPeer())
	sentMessage, err := sender.Media(ctx, message.UploadedDocument(tgFile).
		MIME("application/octet-stream").
		Filename("encrypted_thumbnail"))

	if err != nil {
		return c.Status(500).JSON(fiber.Map{"detail": "Telegram send thumb failed"})
	}

	var thumbMessageID int

	msgStr := fmt.Sprintf("%v", sentMessage)
	importStrParts := splitAndFindID(msgStr)
	if importStrParts > 0 {
		thumbMessageID = importStrParts
	}

	return c.JSON(fiber.Map{
		"status":     "done",
		"message_id": thumbMessageID,
	})
}
