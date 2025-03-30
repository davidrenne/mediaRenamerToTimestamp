package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DanielRenne/GoCore/core/extensions"
	"github.com/DanielRenne/GoCore/core/logger"
	"github.com/DanielRenne/GoCore/core/path"
	"github.com/DanielRenne/GoCore/core/utils"
	"github.com/rwcarlsen/goexif/exif"
)

// ---------------------------------------------------
// Global Configuration
// ---------------------------------------------------
var (
	fmtDesired = "2006-01-02 15.04.05" // default date format
	// Try appending "-1", "-2", etc. when a collision occurs:
	attemptRenameToDifferentMinute = true
	colisionMax                    = 1000000

	pictureExtensions = []string{"JPG", "TIF", "BMP", "PNG", "JPEG", "GIF", "CR2", "ARW", "HEIC", "NEF"}
	movieExtensions   = []string{"MOV", "MP4"}

	backupSuffix = " - Backup Exif"
)

// Apple’s epoch offset for QuickTime metadata
const appleEpochAdjustment = 2082844800

const (
	movieResourceAtomType   = "moov"
	movieHeaderAtomType     = "mvhd"
	referenceMovieAtomType  = "rmra"
	compressedMovieAtomType = "cmov"
)

// ---------------------------------------------------
// Backup Logic
// ---------------------------------------------------

// backupDirectory creates a sibling directory named "<originalName> - Backup Exif"
// and copies all files/folders from the original path (recursively). We use `filepath.Rel`
// to ensure we preserve subfolder structures no matter trailing slashes, OS path separators, etc.
func backupDirectory(originalPath string) (string, error) {
	// Convert to absolute to avoid trailing slash weirdness.
	originalAbsPath, err := filepath.Abs(originalPath)
	if err != nil {
		return "", err
	}

	// Create sibling backup path based on the actual folder name.
	parentDir := filepath.Dir(originalAbsPath)
	baseName := filepath.Base(originalAbsPath)
	backupPath := filepath.Join(parentDir, baseName+backupSuffix)

	err = filepath.Walk(originalAbsPath, func(srcPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Derive the relative sub-path (srcPath minus the originalAbsPath prefix).
		rel, errRel := filepath.Rel(originalAbsPath, srcPath)
		if errRel != nil {
			return errRel
		}
		destPath := filepath.Join(backupPath, rel)

		if info.IsDir() {
			return os.MkdirAll(destPath, os.ModePerm)
		}
		// Copy file contents into the backup.
		input, errRead := os.ReadFile(srcPath)
		if errRead != nil {
			return errRead
		}
		return os.WriteFile(destPath, input, info.Mode())
	})

	if err != nil {
		return "", err
	}
	return backupPath, nil
}

// countFilteredFiles walks a directory, counting only files whose extensions
// are in our photo/video lists. This is how we verify original vs. backup.
func countFilteredFiles(directory string) (int, error) {
	count := 0
	err := filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(info.Name()), "."))
			if inArray(ext, pictureExtensions) || inArray(ext, movieExtensions) {
				count++
			}
		}
		return nil
	})
	return count, err
}

// ---------------------------------------------------
// Helpers
// ---------------------------------------------------

func inArray(value string, array []string) bool {
	for _, v := range array {
		if v == value {
			return true
		}
	}
	return false
}

// recurseFiles returns all files (not directories) under fileDir, recursively.
func recurseFiles(fileDir string) ([]string, error) {
	files := []string{}
	err := filepath.Walk(fileDir, func(path string, f os.FileInfo, errWalk error) error {
		if errWalk != nil {
			return errWalk
		}
		if !f.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// ---------------------------------------------------
// Video Metadata (QuickTime) Extraction
// ---------------------------------------------------

// getVideoCreationTimeMetadata returns the embedded QuickTime/MP4 creation timestamp.
func getVideoCreationTimeMetadata(videoBuffer io.ReadSeeker) (time.Time, error) {
	buf := make([]byte, 8)
	for {
		if _, err := videoBuffer.Read(buf); err != nil {
			return time.Time{}, err
		}
		if bytes.Equal(buf[4:8], []byte(movieResourceAtomType)) {
			break
		}
		atomSize := binary.BigEndian.Uint32(buf)
		if _, err := videoBuffer.Seek(int64(atomSize)-8, io.SeekCurrent); err != nil {
			return time.Time{}, err
		}
	}

	if _, err := videoBuffer.Read(buf); err != nil {
		return time.Time{}, err
	}
	atomType := string(buf[4:8])
	switch atomType {
	case movieHeaderAtomType:
		if _, err := videoBuffer.Read(buf); err != nil {
			return time.Time{}, err
		}
		appleEpoch := int64(binary.BigEndian.Uint32(buf[4:]))
		return time.Unix(appleEpoch-appleEpochAdjustment, 0).Local(), nil
	case compressedMovieAtomType:
		return time.Time{}, errors.New("Compressed video")
	case referenceMovieAtomType:
		return time.Time{}, errors.New("Reference video")
	default:
		return time.Time{}, errors.New("Did not find movie header atom (mvhd)")
	}
}

// renameWithCollision tries renaming, and if the new name already exists, it appends `-1`, `-2`, etc.
func renameWithCollision(src, targetBase, ext string) (string, error) {
	dir := filepath.Dir(src)
	candidate := filepath.Join(dir, targetBase+ext)
	if !extensions.DoesFileExist(candidate) {
		return candidate, nil
	}
	for i := 1; i < colisionMax; i++ {
		candidate = filepath.Join(dir, targetBase+"-"+strconv.Itoa(i)+ext)
		if !extensions.DoesFileExist(candidate) {
			return candidate, nil
		}
	}
	return "", errors.New("no available filename after many attempts")
}

// ---------------------------------------------------
// Core File Processing
// ---------------------------------------------------

func processFile(fileWork string, movieExts []string, stdErr *log.Logger) {
	baseName := filepath.Base(fileWork)
	pieces := strings.Split(baseName, ".")
	if len(pieces) < 2 {
		stdErr.Println("Skipping file without extension: " + fileWork)
		return
	}
	extUpper := strings.ToUpper(pieces[len(pieces)-1])
	extLowerDot := "." + strings.ToLower(pieces[len(pieces)-1]) // e.g. ".jpg" or ".mp4"

	// Handle videos
	if utils.InArray(extUpper, movieExts) {
		fd, err := os.Open(fileWork)
		if err != nil {
			stdErr.Println("Could not open movie file " + fileWork + ": " + err.Error())
			return
		}
		timeInfo, err := getVideoCreationTimeMetadata(fd)
		_ = fd.Close()
		if err != nil {
			stdErr.Println("Could not read timestamp on movie file " + fileWork + ": " + err.Error())
			return
		}
		potentialName := timeInfo.Format(fmtDesired)
		if strings.TrimSuffix(baseName, extLowerDot) != potentialName {
			target, err := renameWithCollision(fileWork, potentialName, extLowerDot)
			if err != nil {
				stdErr.Println("Could not resolve collision for: " + fileWork + ": " + err.Error())
				return
			}
			if err := os.Rename(fileWork, target); err != nil {
				stdErr.Println("Could not rename " + fileWork + " to " + target + ": " + err.Error())
				return
			}
			log.Println("Renamed " + baseName + " => " + filepath.Base(target))
		}
		return
	}

	// Handle images
	data, err := os.ReadFile(fileWork)
	if err != nil {
		stdErr.Println("Could not read file " + fileWork + ": " + err.Error())
		return
	}
	reader := bytes.NewReader(data)
	x, err := exif.Decode(reader)
	if err != nil {
		stdErr.Println("Could not decode EXIF data for " + fileWork + ": " + err.Error())
		return
	}
	jsonBytes, err := x.MarshalJSON()
	if err != nil {
		stdErr.Println("Could not marshal EXIF JSON for " + fileWork + ": " + err.Error())
		return
	}
	exifFields := make(map[string]interface{})
	if err := json.Unmarshal(jsonBytes, &exifFields); err != nil {
		stdErr.Println("Could not unmarshal EXIF JSON for " + fileWork + ": " + err.Error())
		return
	}

	var timeInfo time.Time
	var parseErr error
	if val, ok := exifFields["DateTimeOriginal"]; ok {
		timeInfo, parseErr = time.Parse("2006:01:02 15:04:05", val.(string))
	} else if val, ok := exifFields["DateTime"]; ok {
		timeInfo, parseErr = time.Parse("2006:01:02 15:04:05", val.(string))
	} else {
		stdErr.Println("No suitable EXIF date field found for " + fileWork)
		return
	}
	if parseErr != nil {
		stdErr.Println("Failed to parse EXIF date field for " + fileWork + ": " + parseErr.Error())
		return
	}

	potentialName := timeInfo.Format(fmtDesired)
	if baseName != potentialName+extLowerDot {
		target, err := renameWithCollision(fileWork, potentialName, extLowerDot)
		if err != nil {
			stdErr.Println("Could not resolve collision for " + fileWork + ": " + err.Error())
			return
		}
		if err := os.Rename(fileWork, target); err != nil {
			stdErr.Println("Could not rename " + fileWork + " to " + target + ": " + err.Error())
			return
		}
		log.Println("Renamed " + baseName + " => " + filepath.Base(target))
	}
}

// processDirectory is an optional helper if you prefer to process an entire
// folder at once, but here we do it file-by-file in main().
func processDirectory(fileDir string, stdErr *log.Logger) {
	files, err := recurseFiles(fileDir)
	if err != nil {
		stdErr.Println("Error walking directory " + fileDir + ": " + err.Error())
		return
	}
	for _, fileWork := range files {
		processFile(fileWork, movieExtensions, stdErr)
	}
}

// countFilesInDirs returns the total number of filtered (photo/video) files in each directory.
func countFilesInDirs(originalDir, backupDir string) (int, int, error) {
	originalCount, err1 := countFilteredFiles(originalDir)
	if err1 != nil {
		return 0, 0, err1
	}
	backupCount, err2 := countFilteredFiles(backupDir)
	if err2 != nil {
		return 0, 0, err2
	}
	return originalCount, backupCount, nil
}

// ---------------------------------------------------
// main()
// ---------------------------------------------------

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: program <directory> [date-format]")
	}
	potentialPath := os.Args[1]
	if len(os.Args) == 3 {
		fmtDesired = os.Args[2]
	}

	startEntireProcess := time.Now()
	stdErr := log.New(os.Stderr, "", 0)

	// 1) Convert the user’s path to absolute to avoid trailing slash edge cases
	originalAbsPath, err := filepath.Abs(potentialPath)
	if err != nil {
		log.Fatal("Failed to get absolute path: ", err)
	}

	if !extensions.DoesFileExist(originalAbsPath) {
		log.Fatal("Path does not exist or is invalid: ", originalAbsPath)
	}

	// 2) Create a backup as a sibling of the original directory
	backupDirPath, err := backupDirectory(originalAbsPath)
	if err != nil {
		log.Fatalf("Backup failed: %v", err)
	}
	log.Println("Backup created at:", backupDirPath)

	// 3) Recursively find and rename all matching files
	files, err := recurseFiles(originalAbsPath)
	if err != nil {
		log.Fatal("Error recursing files: ", err)
	}
	for _, fileToWorkOn := range files {
		ext := strings.ToUpper(filepath.Ext(fileToWorkOn))
		ext = strings.TrimPrefix(ext, ".") // remove leading "."
		if inArray(ext, pictureExtensions) || inArray(ext, movieExtensions) {
			baseName := filepath.Base(fileToWorkOn)
			// If the base name (minus .ext) already *parses* into the fmtDesired, skip
			nameNoExt := strings.TrimSuffix(baseName, "."+strings.ToLower(ext))
			if _, parseErr := time.Parse(fmtDesired, nameNoExt); parseErr == nil {
				log.Println(baseName + " is already in desired date format, skipping.")
				continue
			}
			// Otherwise, process (may rename).
			processFile(fileToWorkOn, movieExtensions, stdErr)
		}
	}

	// 4) Compare total counts in original vs backup; remove backup if counts match.
	originalCount, backupCount, err := countFilesInDirs(originalAbsPath, backupDirPath)
	if err != nil {
		log.Printf("Error counting files: %v", err)
	} else if originalCount == backupCount {
		_ = os.RemoveAll(backupDirPath)
		log.Printf("Backup removed: %s (counts matched: %d)", backupDirPath, originalCount)
	} else {
		log.Printf("Backup retained due to mismatch: %s (Original: %d, Backup: %d)",
			backupDirPath, originalCount, backupCount)
	}

	log.Println(logger.TimeTrack(startEntireProcess, "Completed in"))
}
