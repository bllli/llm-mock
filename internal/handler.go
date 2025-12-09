package internal

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

var requestDecoder = sonic.ConfigFastest

type MockRequest struct {
	ReqID        string
	Start        time.Time
	modelConfig  ModelConfig
	PromptTokens int
	OutputTokens int
	Choices      *[]Token
}

var (
	HandlerEnterCount  atomic.Uint64
	HandlerActiveCount atomic.Int64
)

func ModelsHandler(c *gin.Context) {
	models := make([]string, 0, len(AppConfig.ModelMap))
	for name := range AppConfig.ModelMap {
		models = append(models, name)
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

func ChatHandler(c *gin.Context) {

	// Increment Prometheus active requests gauge
	IncrementActiveRequests()
	defer DecrementActiveRequests()

	if AppConfig.Server.PprofEnabled {
		HandlerEnterCount.Add(1)
		HandlerActiveCount.Add(1)
		defer HandlerActiveCount.Add(-1)
	}

	reqID := uuid.New().String()

	// Track this request for graceful shutdown
	AddRequest(reqID)
	defer RemoveRequest(reqID)

	// Check if server is shutting down
	if IsShuttingDown() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Server is shutting down"})
		IncrementRequestTotal("503", "rejected")
		return
	}

	start := time.Now()
	var request ChatRequest
	err := requestDecoder.NewDecoder(c.Request.Body).Decode(&request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		IncrementRequestTotal("400", "rejected")
		return
	}
	modelConfig, ok := AppConfig.ModelMap[request.Model]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Model not found"})
		IncrementRequestTotal("400", "rejected")
		return
	}
	maxTokens := min(request.MaxTokens, modelConfig.MaxOutputTokens)

	prompt := strings.Builder{}
	for _, message := range request.Messages {
		prompt.Write(message.Content)
	}

	promptTokens := CountTokensFast(prompt.String())
	if promptTokens > modelConfig.MaxContextTokens {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Prompt tokens exceed max context tokens"})
		return
	}

	maxTokens = min(maxTokens, modelConfig.MaxContextTokens-promptTokens)

	if maxTokens <= 0 {
		maxTokens = 50
	}

	choices := GetTokens(promptTokens, maxTokens)

	mockRequest := MockRequest{
		ReqID:        reqID,
		Start:        start,
		modelConfig:  modelConfig,
		PromptTokens: promptTokens,
		OutputTokens: maxTokens,
		Choices:      choices,
	}

	// Logger.Info("PreProcess time cost", zap.Duration("cost", time.Since(start)))

	// Determine stream type for metrics
	streamType := "stream"
	if !request.Stream {
		streamType = "non-stream"
	}

	if request.Stream {
		if err := handleStreamChat(c, &mockRequest); err != nil {
			IncrementRequestTotal("500", streamType)
			return
		}
		IncrementRequestTotal("200", streamType)
	} else {
		if err := handleNormalChat(c, &mockRequest); err != nil {
			IncrementRequestTotal("500", streamType)
			return
		}
		IncrementRequestTotal("200", streamType)
	}
}

func handleNormalChat(c *gin.Context, mockRequest *MockRequest) error {
	ttft := mockRequest.modelConfig.TTFT.GetTTFT()
	time.Sleep(time.Duration(ttft) * time.Millisecond)

	tpot_ms := 1000 / (mockRequest.modelConfig.OTPS + 1)
	tpot := time.Duration(tpot_ms) * time.Millisecond

	content := strings.Builder{}
	first := true
	for _, choice := range *mockRequest.Choices {
		if !first {
			time.Sleep(tpot)
		}
		first = false
		content.WriteString(choice.Content)
	}

	b, err := MarshalCompletion(mockRequest.ReqID, time.Now(), mockRequest.modelConfig.Name, content.String(),
		mockRequest.PromptTokens, mockRequest.OutputTokens, mockRequest.PromptTokens+mockRequest.OutputTokens)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return err
	}

	c.Data(http.StatusOK, "application/json", *b)
	return nil
}

func handleStreamChat(c *gin.Context, mockRequest *MockRequest) error {
	// Set SSE headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	ttft := mockRequest.modelConfig.TTFT.GetTTFT()
	time.Sleep(time.Duration(ttft) * time.Millisecond)

	tpot_ms := 1000 / (mockRequest.modelConfig.OTPS + 1)
	tpot := time.Duration(tpot_ms) * time.Millisecond

	first := true
	completionTokens := 0
	var err error

	// Stream each chunk with proper SSE format
	for _, choice := range *mockRequest.Choices {
		// Check if server is shutting down mid-stream
		if IsShuttingDown() {
			Logger.Warn("Server shutting down, terminating stream early",
				zap.String("requestID", mockRequest.ReqID))
			break
		}

		if !first {
			time.Sleep(tpot)
		}
		first = false
		completionTokens += choice.Tokens

		chunk, err := MarshalChunk(
			mockRequest.ReqID,
			time.Now(),
			mockRequest.modelConfig.Name,
			choice.Content,
			mockRequest.PromptTokens,
			completionTokens,
			completionTokens+mockRequest.PromptTokens,
		)
		if err != nil {
			Logger.Error("Failed to marshal chunk", zap.Error(err))
			return err
		}

		// Write SSE formatted data
		if _, err := c.Writer.Write([]byte("data: ")); err != nil {
			Logger.Error("Failed to write data prefix", zap.Error(err))
			IncrementFlushErrors()
			return err
		}
		if _, err := c.Writer.Write(*chunk); err != nil {
			Logger.Error("Failed to write chunk data", zap.Error(err))
			IncrementFlushErrors()
			return err
		}
		if _, err := c.Writer.Write([]byte("\n\n")); err != nil {
			Logger.Error("Failed to write chunk separator", zap.Error(err))
			IncrementFlushErrors()
			return err
		}

		// Flush to ensure immediate delivery to client
		c.Writer.Flush()
		if f, ok := c.Writer.(interface{ FlushError() error }); ok {
			if err := f.FlushError(); err != nil {
				Logger.Error("Failed to flush chunk to client", zap.Error(err))
				IncrementFlushErrors()
				return err
			}
		}
	}

	// Send final chunk with empty content
	lastChunk, err := MarshalChunk(
		mockRequest.ReqID,
		time.Now(),
		mockRequest.modelConfig.Name,
		"",
		mockRequest.PromptTokens,
		completionTokens,
		completionTokens+mockRequest.PromptTokens,
	)
	if err != nil {
		Logger.Error("Failed to marshal final chunk", zap.Error(err))
		return err
	}

	// Write final chunk
	if _, err := c.Writer.Write([]byte("data: ")); err != nil {
		Logger.Error("Failed to write final data prefix", zap.Error(err))
		IncrementFlushErrors()
		return err
	}
	if _, err := c.Writer.Write(*lastChunk); err != nil {
		Logger.Error("Failed to write final chunk data", zap.Error(err))
		IncrementFlushErrors()
		return err
	}
	if _, err := c.Writer.Write([]byte("\n\n")); err != nil {
		Logger.Error("Failed to write final chunk separator", zap.Error(err))
		IncrementFlushErrors()
		return err
	}

	// Send DONE marker
	if _, err := c.Writer.Write([]byte("data: [DONE]\n\n")); err != nil {
		Logger.Error("Failed to write DONE marker", zap.Error(err))
		IncrementFlushErrors()
		return err
	}

	// Final flush
	c.Writer.Flush()
	if f, ok := c.Writer.(interface{ FlushError() error }); ok {
		if err := f.FlushError(); err != nil {
			Logger.Error("Failed to final flush", zap.Error(err))
			IncrementFlushErrors()
			return err
		}
	}

	return nil
}
