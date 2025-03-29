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

var (
	// Desired date format for renaming.
	fmtDesired = "2006-01-02 15.04.05"
	// If true, try appending "-1", "-2", etc. when a collision occurs.
	attemptRenameToDifferentMinute = true
	// Maximum number of attempts to resolve collisions.
	colisionMax       = 1000000
	pictureExtensions = []string{"JPG", "TIF", "BMP", "PNG", "JPEG", "GIF", "CR2", "ARW", "HEIC", "NEF"}
	movieExtensions   = []string{"MOV", "MP4"}
	backupSuffix      = " - Backup Exif"
)

const appleEpochAdjustment = 2082844800

const (
	movieResourceAtomType   = "moov"
	movieHeaderAtomType     = "mvhd"
	referenceMovieAtomType  = "rmra"
	compressedMovieAtomType = "cmov"
)

func backupDirectory(originalPath string) (string, error) {
	backupPath := filepath.Join(filepath.Dir(originalPath), filepath.Base(originalPath)+backupSuffix)
	err := filepath.Walk(originalPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		backupFilePath := filepath.Join(backupPath, strings.TrimPrefix(path, originalPath))
		if info.IsDir() {
			return os.MkdirAll(backupFilePath, os.ModePerm)
		}
		input, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(backupFilePath, input, info.Mode())
	})
	return backupPath, err
}

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

func inArray(value string, array []string) bool {
	for _, v := range array {
		if v == value {
			return true
		}
	}
	return false
}

func recurseFiles(fileDir string) (files []string, err error) {
	err = filepath.Walk(fileDir, func(path string, f os.FileInfo, errWalk error) error {
		if errWalk != nil {
			return errWalk
		}
		if !f.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return
}

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

func processFile(fileWork string, movieExtensions []string, stdErr *log.Logger) {
	baseName := filepath.Base(fileWork)
	pieces := strings.Split(baseName, ".")
	if len(pieces) < 2 {
		stdErr.Println("Skipping file without extension: " + fileWork)
		return
	}
	extUpper := strings.ToUpper(pieces[len(pieces)-1])
	ext := "." + strings.ToLower(pieces[len(pieces)-1])

	if utils.InArray(extUpper, movieExtensions) {
		fd, err := os.Open(fileWork)
		if err != nil {
			stdErr.Println("Could not open movie file " + fileWork + ": " + err.Error())
			return
		}
		timeInfo, err := getVideoCreationTimeMetadata(fd)
		fd.Close()
		if err != nil {
			stdErr.Println("Could not read timestamp on movie file " + fileWork + ": " + err.Error())
			return
		}
		potentialName := timeInfo.Format(fmtDesired)
		if strings.TrimSuffix(baseName, ext) != potentialName {
			target, err := renameWithCollision(fileWork, potentialName, ext)
			if err != nil {
				stdErr.Println("Could not resolve collision for: " + fileWork + ": " + err.Error())
				return
			}
			if err := os.Rename(fileWork, target); err != nil {
				stdErr.Println("Could not rename " + fileWork + " to " + target + ": " + err.Error())
				return
			}
			log.Println("Renamed " + baseName + " to " + filepath.Base(target))
		}
		return
	}

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
	data, err = x.MarshalJSON()
	if err != nil {
		stdErr.Println("Could not marshal EXIF JSON for " + fileWork + ": " + err.Error())
		return
	}
	exifFields := make(map[string]interface{})
	if err := json.Unmarshal(data, &exifFields); err != nil {
		stdErr.Println("Could not unmarshal EXIF JSON for " + fileWork + ": " + err.Error())
		return
	}

	var timeInfo time.Time
	var parseErr error
	if val, ok := exifFields["DateTimeOriginal"]; ok {
		timeInfo, parseErr = time.Parse("2006:01:02 15:04:05", val.(string))
		if parseErr != nil {
			stdErr.Println("Failed to parse DateTimeOriginal EXIF data for " + fileWork + ": " + parseErr.Error())
			return
		}
	} else if val, ok := exifFields["DateTime"]; ok {
		timeInfo, parseErr = time.Parse("2006:01:02 15:04:05", val.(string))
		if parseErr != nil {
			stdErr.Println("Failed to parse DateTime EXIF data for " + fileWork + ": " + parseErr.Error())
			return
		}
	} else {
		stdErr.Println("No suitable date field found for " + fileWork)
		return
	}

	potentialName := timeInfo.Format(fmtDesired)
	if baseName != potentialName+ext {
		target, err := renameWithCollision(fileWork, potentialName, ext)
		if err != nil {
			stdErr.Println("Could not resolve collision for " + fileWork + ": " + err.Error())
			return
		}
		if err := os.Rename(fileWork, target); err != nil {
			stdErr.Println("Could not rename " + fileWork + " to " + target + ": " + err.Error())
			return
		}
		log.Println("Renamed " + baseName + " to " + filepath.Base(target))
	}
}

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

func countFilesInDirs(originalDir, backupDir string) (int, int, error) {
	originalCount, err1 := countFilteredFiles(originalDir)
	backupCount, err2 := countFilteredFiles(backupDir)

	if err1 != nil {
		return 0, 0, err1
	}
	if err2 != nil {
		return 0, 0, err2
	}

	return originalCount, backupCount, nil
}

func main() {
	// Validate command-line arguments.
	if len(os.Args) < 2 {
		log.Fatal("Usage: program <directory> [date-format]")
	}
	potentialPath := os.Args[1]
	if len(os.Args) == 3 {
		fmtDesired = os.Args[2]
	}

	startEntireProcess := time.Now()
	stdErr := log.New(os.Stderr, "", 0)

	// Ensure the directory path ends with the appropriate separator.
	var directoryToIterate string
	lastByte := potentialPath[len(potentialPath)-1:]
	if lastByte != "\\" && path.IsWindows {
		directoryToIterate = potentialPath + "\\"
	} else if lastByte != "/" {
		directoryToIterate = potentialPath + "/"
	} else {
		directoryToIterate = potentialPath
	}

	if path.IsWindows && strings.Contains(directoryToIterate, "\\\\") {
		log.Fatal("Please only escape your directory path once with \\")
	}

	if !extensions.DoesFileExist(directoryToIterate) {
		log.Fatal("Path does not exist or is invalid")
	}
	backupDirPath, err := backupDirectory(directoryToIterate)
	if err != nil {
		log.Fatalf("Backup failed: %v", err) // Logs the error and exits
	}
	log.Println("Backup created at:", backupDirPath)

	// Get all files in the directory tree.
	files, err := recurseFiles(directoryToIterate)
	if err != nil {
		log.Fatal("Error recursing files: ", err)
	}

	// Process each file synchronously.
	for _, fileToWorkOn := range files {
		// Only process files with a known extension.
		extParts := strings.Split(fileToWorkOn, ".")
		if len(extParts) < 2 {
			continue
		}

		ext := strings.ToUpper(extParts[len(extParts)-1])
		if utils.InArray(ext, pictureExtensions) || utils.InArray(ext, movieExtensions) {
			baseName := filepath.Base(fileToWorkOn)
			pieces := strings.Split(baseName, ".")
			// Skip files that already match the desired format.
			if _, err := time.Parse(fmtDesired, strings.TrimSuffix(baseName, "."+pieces[len(pieces)-1])); err == nil {
				log.Println(baseName + " is in desired date format, skipping.")
				continue
			}
			processFile(fileToWorkOn, movieExtensions, stdErr)
		}
	}

	// // Perform the cleanup of backup files.
	// cleanupBackups(backupDirPath)wsl -l -v

	originalCount, backupCount, err := countFilesInDirs(directoryToIterate, backupDirPath)
	if err == nil && originalCount == backupCount {
		os.RemoveAll(backupDirPath)
		log.Printf("Backup removed: %s", backupDirPath)
	} else if err != nil {
		log.Printf("Error counting files: %v", err)
	} else {
		log.Printf("Backup retained due to mismatch: %s (Original: %d, Backup: %d)", backupDirPath, originalCount, backupCount)
	}

	log.Println(logger.TimeTrack(startEntireProcess, "Completed in"))
}
