package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"

	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)

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

	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error reading video metadata from db", err)
		return
	}

	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "user not the owner", nil)
		return
	}

	// Retrieve the file from the form
	file, header, err := r.FormFile("video") // "video" is the name of the file input in the HTML form
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't retrieve file from the form", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "not a video/mp4", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "file type must be video/mp4", nil)
		return
	}

	// Create a new file on the server to store the uploaded content
	dst, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error creating destination file", err)
		return
	}
	defer dst.Close()
	defer os.Remove(dst.Name())

	// Copy the uploaded file content to the new file
	_, err = io.Copy(dst, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error copying file content", err)
		return
	}
	aspectRatio, err := getVideoAspectRatio(dst.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "could not retrieve aspect ratio", err)
		return
	}

	processedFastFile, err := processVideoForFastStart(dst.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "could not process fast file", err)
		return
	}

	fastFile, err := os.Open(processedFastFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error opening temp file", err)
		return
	}
	defer fastFile.Close()
	defer os.Remove(processedFastFile)

	_, err = fastFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error seeking fastFile", err)
		return
	}

	// Generate 32 random bytes and encode them to create a unique ID.
	key := make([]byte, 32)
	_, err = rand.Read(key)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to rand read", err)
		return
	}

	fileKey := aspectRatio + "/" + hex.EncodeToString(key) + ".mp4"

	putObjectInput := &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(fileKey),
		Body:        fastFile,
		ContentType: aws.String(mediaType),
	}

	_, err = cfg.s3Client.PutObject(context.Background(), putObjectInput)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error putting object in s3", err)
		return
	}
	VideoURL := fmt.Sprintf("%v,%v", cfg.s3Bucket, fileKey)

	videoMetadata.VideoURL = &VideoURL

	err = cfg.db.UpdateVideo(videoMetadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error updating video", err)
		return
	}
	signed, err := cfg.dbVideoToSignedVideo(videoMetadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to presign video", err)
		return
	}
	respondWithJSON(w, http.StatusOK, signed)
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s3Client)
	params := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}
	presigned, err := presignClient.PresignGetObject(context.Background(), params, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", err
	}
	return presigned.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil || !strings.Contains(*video.VideoURL, ",") {
		return video, fmt.Errorf("invalid video_url format")
	}
	parts := strings.SplitN(*video.VideoURL, ",", 2)
	bucket, key := parts[0], parts[1]
	presignedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, 2*time.Minute)
	if err != nil {
		return video, err
	}

	video.VideoURL = &presignedURL

	return video, nil
}
