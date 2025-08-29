package main

// This file defines the HTTP handler responsible for processing
// thumbnail image uploads for a specific video. It validates the
// request (path params, auth token, media type), persists the uploaded
// file to the local assets directory using a unique filename, updates
// the video's metadata with a public URL to the thumbnail, and returns
// the updated video as JSON.

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
    // Parse the video ID from the request path and validate it.
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

    // Extract and validate the Bearer token, then resolve the user ID
    // from the JWT using the configured secret.
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	// "thumbnail" should match the HTML form input name
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

    // Validate that the uploaded content is a supported image type.
	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", err)
		return
	}
	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "format invalid, must use jpeg or png", err)
		return
	}

	mediaType = strings.Split(mediaType, "/")[1]
	// Generate 32 random bytes and encode them to create a unique ID.
	key := make([]byte, 32)
	_, err = rand.Read(key)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to rand read", err)
		return
	}
	uniqueID := base64.RawURLEncoding.EncodeToString(key)
	uniqueFilePath := filepath.Join(cfg.assetsRoot, fmt.Sprintf("%s.%s", uniqueID, mediaType))
	f, err := os.Create(uniqueFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create unique file", err)
		return
	}
	defer f.Close()

    // Stream the uploaded file to disk at the unique path.
	_, err = io.Copy(f, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to copy file", err)
		return
	}

    // Fetch the video to ensure it exists and belongs to the user.
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You do not have permission to upload a thumbnail for this video", nil)
		return
	}

    // Construct the public URL for the newly stored thumbnail and
    // persist it to the video's metadata.
	url := fmt.Sprintf("http://localhost:%s/assets/%s.%s", cfg.port, uniqueID, mediaType)
	video.ThumbnailURL = &url

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video metadata", err)
		return
	}

    // Respond with the updated video record.
	respondWithJSON(w, http.StatusOK, video)
}
