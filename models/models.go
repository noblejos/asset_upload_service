package models

type UploadRequest struct {
	AWSAccessKeyID     string `form:"aws_access_key_id" binding:"required"`
	AWSSecretAccessKey string `form:"aws_secret_access_key" binding:"required"`
	AWSRegion          string `form:"aws_region" binding:"required"`
	S3BucketName       string `form:"s3_bucket_name" binding:"required"`
}

type MediaFormat struct {
	Name        string  `json:"name"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	AspectRatio float64 `json:"aspect_ratio"`
}

type FileInfo struct {
	FileType      string  `json:"file_type"`
	Width         int     `json:"width,omitempty"`
	Height        int     `json:"height,omitempty"`
	OriginalRatio float64 `json:"original_ratio,omitempty"`
	AspectRatio   string  `json:"aspect_ratio,omitempty"`
	MatchedFormat string  `json:"matched_format,omitempty"`
	Duration      float64 `json:"duration,omitempty"`
	VideoCodec    string  `json:"video_codec,omitempty"`
	AudioCodec    string  `json:"audio_codec,omitempty"`
	FrameRate     float64 `json:"frame_rate,omitempty"`
}

type UploadResponse struct {
	FileName      string  `json:"file_name"`
	FileURL       string  `json:"file_url"`
	FileType      string  `json:"file_type"`
	FileSize      int64   `json:"file_size"`
	Width         int     `json:"width,omitempty"`
	Height        int     `json:"height,omitempty"`
	OriginalRatio float64 `json:"original_ratio,omitempty"`
	AspectRatio   string  `json:"aspect_ratio,omitempty"`
	MatchedFormat string  `json:"matched_format,omitempty"`
	Duration      float64 `json:"duration,omitempty"`
	Message       string  `json:"message"`
}
