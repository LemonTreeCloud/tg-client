package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"tg-client/internal/config"
	tgservice "tg-client/internal/telegram"
)

type TelegramService interface {
	Auth() *tgservice.AuthBroker
	IsReady() bool
	ConnectionState() string
	Account(ctx context.Context) (tgservice.AccountInfo, error)
	ListDialogs(ctx context.Context, limit int) ([]tgservice.DialogInfo, error)
	Dialog(ctx context.Context, chatID int64) (tgservice.DialogInfo, error)
	RecentMessages(ctx context.Context, chatID int64, limit int) ([]tgservice.MessageInfo, error)
	SendMessage(ctx context.Context, chatID int64, text string) error
}

type Server struct {
	config  config.HTTPConfig
	tz      *time.Location
	service TelegramService
	logger  *slog.Logger
	handler http.Handler
}

func New(cfg config.HTTPConfig, timezone *time.Location, service TelegramService, logger *slog.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	_ = router.SetTrustedProxies(nil)
	server := &Server{config: cfg, tz: timezone, service: service, logger: logger}
	router.Use(gin.Recovery(), server.requestLogger())
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	api := router.Group("/api/v1", server.requireAPIKey())
	api.GET("/status", server.status)
	api.GET("/auth/code", server.authStatus)
	api.POST("/auth/code", server.submitCode)
	api.GET("/account", server.account)
	api.GET("/dialogs", server.dialogs)
	api.GET("/dialogs/:chat_id", server.dialog)
	api.GET("/dialogs/:chat_id/messages", server.messages)
	api.POST("/dialogs/:chat_id/messages", server.sendMessage)
	server.handler = router
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) Run(ctx context.Context) error {
	httpServer := &http.Server{
		Addr:              s.config.ListenAddress(),
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      75 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("HTTP server shutdown failed", "error", err)
		}
	}()
	s.logger.Info("HTTP API is listening", "address", s.config.ListenAddress())
	err := httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownDone
		return nil
	}
	return err
}

func (s *Server) requireAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		provided := c.GetHeader("X-API-Key")
		if provided == "" {
			authorization := c.GetHeader("Authorization")
			if len(authorization) > 7 && strings.EqualFold(authorization[:7], "Bearer ") {
				provided = authorization[7:]
			}
		}
		matched := 0
		for _, expected := range s.config.APIKeys {
			matched |= subtle.ConstantTimeCompare([]byte(provided), []byte(expected))
		}
		if matched != 1 {
			writeError(c, http.StatusUnauthorized, "unauthorized", "a valid API key is required")
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		s.logger.Info("HTTP request", "method", c.Request.Method, "path", c.FullPath(), "status", c.Writer.Status(), "duration", time.Since(started))
	}
}

func (s *Server) status(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"auth":             s.service.Auth().Snapshot(),
		"ready":            s.service.IsReady(),
		"connection_state": s.service.ConnectionState(),
		"timezone":         s.tz.String(),
	}})
}

func (s *Server) authStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": s.service.Auth().Snapshot()})
}

func (s *Server) submitCode(c *gin.Context) {
	var input struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "JSON field code is required")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	err := s.service.Auth().SubmitCode(ctx, input.Code)
	if errors.Is(err, tgservice.ErrNotWaitingForCode) {
		writeErrorWithData(c, http.StatusConflict, "not_waiting_for_code", err.Error(), s.service.Auth().Snapshot())
		return
	}
	if err != nil {
		writeErrorWithData(c, http.StatusUnauthorized, "login_failed", err.Error(), s.service.Auth().Snapshot())
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": s.service.Auth().Snapshot()})
}

func (s *Server) account(c *gin.Context) {
	account, err := s.service.Account(c.Request.Context())
	if err != nil {
		s.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": account})
}

func (s *Server) dialogs(c *gin.Context) {
	limit, ok := parseLimit(c)
	if !ok {
		return
	}
	dialogs, err := s.service.ListDialogs(c.Request.Context(), limit)
	if err != nil {
		s.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": dialogs, "count": len(dialogs)})
}

func (s *Server) dialog(c *gin.Context) {
	chatID, ok := parseChatID(c)
	if !ok {
		return
	}
	dialog, err := s.service.Dialog(c.Request.Context(), chatID)
	if err != nil {
		s.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": dialog})
}

func (s *Server) messages(c *gin.Context) {
	chatID, ok := parseChatID(c)
	if !ok {
		return
	}
	limit, ok := parseLimit(c)
	if !ok {
		return
	}
	messages, err := s.service.RecentMessages(c.Request.Context(), chatID, limit)
	if err != nil {
		s.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": messages, "count": len(messages)})
}

func (s *Server) sendMessage(c *gin.Context) {
	chatID, ok := parseChatID(c)
	if !ok {
		return
	}
	var input struct {
		Message string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "JSON field message is required")
		return
	}
	if err := s.service.SendMessage(c.Request.Context(), chatID, input.Message); err != nil {
		s.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"sent": true, "chat_id": chatID}})
}

func (s *Server) writeServiceError(c *gin.Context, err error) {
	status := http.StatusBadGateway
	code := "telegram_error"
	if !s.service.IsReady() {
		status = http.StatusServiceUnavailable
		code = "telegram_not_ready"
	} else if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not present") {
		status = http.StatusNotFound
		code = "dialog_not_found"
	} else if strings.Contains(err.Error(), "limit") || strings.Contains(err.Error(), "must not be empty") {
		status = http.StatusBadRequest
		code = "invalid_request"
	}
	writeError(c, status, code, err.Error())
}

func parseChatID(c *gin.Context) (int64, bool) {
	chatID, err := strconv.ParseInt(c.Param("chat_id"), 10, 64)
	if err != nil || chatID == 0 {
		writeError(c, http.StatusBadRequest, "invalid_chat_id", "chat_id must be a non-zero signed 64-bit integer")
		return 0, false
	}
	return chatID, true
}

func parseLimit(c *gin.Context) (int, bool) {
	value := c.Query("limit")
	if value == "" {
		return 0, true
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 {
		writeError(c, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
		return 0, false
	}
	return limit, true
}

func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

func writeErrorWithData(c *gin.Context, status int, code, message string, data any) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}, "data": data})
}
