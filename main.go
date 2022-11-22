package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DanielRenne/GoCore/core/extensions"
	"github.com/DanielRenne/GoCore/core/logger"
	"github.com/DanielRenne/GoCore/core/path"
	"github.com/DanielRenne/GoCore/core/utils"
	"github.com/rwcarlsen/goexif/exif"
)

type filesSync struct {
	sync.Mutex
	Items []string
}

func RecurseFiles(fileDir string) (files []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			return
		}
	}()

	var wg sync.WaitGroup
	var syncedItems filesSync
	path := fileDir

	if extensions.DoesFileExist(path) == false {
		return
	}

	err = filepath.Walk(path, func(path string, f os.FileInfo, errWalk error) (err error) {

		if errWalk != nil {
			err = errWalk
			return
		}

		if !f.IsDir() {
			wg.Add(1)
			syncedItems.Lock()
			syncedItems.Items = append(syncedItems.Items, path)
			syncedItems.Unlock()
			wg.Done()
		}

		return
	})
	wg.Wait()
	files = syncedItems.Items

	return
}

type processJob struct {
	Func func(string)
	File string
	Wg   *sync.WaitGroup
}

var (
	jobs                           chan processJob
	fmtDesired                     string
	attemptRenameToDifferentMinute bool // set to false if you dont want this desire
)

func init() {
	attemptRenameToDifferentMinute = true
	numConcurrent := 100
	jobs = make(chan processJob)
	for i := 0; i < numConcurrent; i++ {
		go worker(i)
	}
}

func worker(idx int) {
	defer func() {
		if r := recover(); r != nil {
			return
		}
	}()

	for job := range jobs {
		job.Func(job.File)
		job.Wg.Done()
	}
}

func main() {
	potentialPath := os.Args[1]
	if len(os.Args) == 3 {
		fmtDesired = os.Args[2]
	} else {
		fmtDesired = "2006-01-02 15.04.05"
	}
	startEntireProcess := time.Now()
	stdErr := log.New(os.Stderr, "", 0)
	if len(os.Args) < 2 {
		log.Fatal("Please pass your MP3 directory to process")
	}
	var directoryToIterate string
	var processJobs []processJob
	var wg sync.WaitGroup

	lastByte := potentialPath[len(potentialPath)-1:]
	if lastByte != "\\" && path.IsWindows {
		directoryToIterate = potentialPath + "\\"
	} else if lastByte != "/" {
		directoryToIterate = potentialPath + "/"
	}

	if path.IsWindows && strings.Index(directoryToIterate, "\\\\") != -1 {
		log.Fatal("Please only escape your directory path once with \\")
	}

	if extensions.DoesFileExist(directoryToIterate) == false {
		log.Fatal("Path does not exist or is invalid")
	}
	pictureExtensions := []string{
		"JPG", "TIF", "BMP", "PNG", "JPEG", "GIF", "CR2", "ARW", "HEIC", "NEF",
	}
	files, _ := RecurseFiles(directoryToIterate)
	for _, fileToWorkOn := range files {
		pieces := strings.Split(fileToWorkOn, ".")
		ext := strings.ToUpper(pieces[len(pieces)-1:][0])
		if utils.InArray(ext, pictureExtensions) {

			pieces := strings.Split(filepath.Base(fileToWorkOn), ".")
			existingExt := "." + pieces[len(pieces)-1:][0]
			fileName := strings.ReplaceAll(filepath.Base(fileToWorkOn), existingExt, "")
			_, err := time.Parse(fmtDesired, fileName)
			if err == nil {
				log.Println(fileName + " is in desired date format skipping")
				continue
			}

			processJobs = append(processJobs, processJob{
				Wg:   &wg,
				File: fileToWorkOn,
				Func: func(fileWork string) {
					pieces := strings.Split(filepath.Base(fileWork), ".")
					existingExt := "." + pieces[len(pieces)-1:][0]
					data, err := os.ReadFile(fileWork)
					if err != nil {
						stdErr.Println("Could not ReadFile" + fileWork + ": " + err.Error())
						return
					}
					reader := bytes.NewReader(data)
					x, err := exif.Decode(reader)
					if err != nil {
						stdErr.Println("Could not exif.Decode " + fileWork + ": " + err.Error())
						return
					}
					data, err = x.MarshalJSON()
					if err != nil {
						stdErr.Println("Could not MarshalJSON " + fileWork + ": " + err.Error())
						return
					}
					exifFields := make(map[string]interface{})
					json.Unmarshal(data, &exifFields)
					dateTimeOriginalValue, dateTimeOriginalok := exifFields["DateTimeOriginal"]
					dateTimeValue, dateTimeok := exifFields["DateTime"]
					if dateTimeOriginalok {
						timeInfo, err := time.Parse("2006:01:02 15:04:05", dateTimeOriginalValue.(string))
						if err != nil {
							stdErr.Println("Failed to parse DateTimeOriginal Exif Data: " + fileWork + ": " + err.Error())
							return
						}
						potentialName := timeInfo.Format(fmtDesired)
						fileName := strings.ReplaceAll(filepath.Base(fileWork), existingExt, "")
						if fileName != potentialName {
							newName := strings.ReplaceAll(fileWork, path.PathSeparator+fileName+existingExt, path.PathSeparator+potentialName+existingExt)
							err := os.Rename(fileWork, newName)
							if err != nil {
								if attemptRenameToDifferentMinute {
									for i := 1; i < 100000; i++ {
										potentialName := potentialName + "-" + extensions.IntToString(i)
										newName = strings.ReplaceAll(fileWork, path.PathSeparator+fileName+existingExt, path.PathSeparator+potentialName+existingExt)
										if err := os.Rename(fileWork, newName); err == nil {
											log.Println("Renamed " + fileName + " to " + potentialName)
											return
										}
									}
								}
								stdErr.Println("Could not rename: " + fileWork + ": " + err.Error())
								return
							}
							log.Println("Renamed " + fileName + " to " + potentialName)
						}
					} else if dateTimeok {
						timeInfo, err := time.Parse("2006:01:02 15:04:05", dateTimeValue.(string))
						if err != nil {
							stdErr.Println("Failed to parse DateTime Exif Data: " + fileWork + ": " + err.Error())
							return
						}
						potentialName := timeInfo.Format(fmtDesired)
						fileName := strings.ReplaceAll(filepath.Base(fileWork), existingExt, "")
						if fileName != potentialName {
							newName := strings.ReplaceAll(fileWork, path.PathSeparator+fileName+existingExt, path.PathSeparator+potentialName+existingExt)
							err := os.Rename(fileWork, newName)
							if err != nil {
								if attemptRenameToDifferentMinute {
									for i := 1; i < 100000; i++ {
										potentialName := potentialName + "-" + extensions.IntToString(i)
										newName = strings.ReplaceAll(fileWork, path.PathSeparator+fileName+existingExt, path.PathSeparator+potentialName+existingExt)
										if err := os.Rename(fileWork, newName); err == nil {
											log.Println("Renamed " + fileName + " to " + potentialName)
											return
										}
									}
								}
								stdErr.Println("Could not rename: " + fileWork + ": " + err.Error())
								return
							}
							log.Println("Renamed " + fileName + " to " + potentialName)
						}
					}
				},
			})
		}
	}
	wg.Add(len(processJobs))
	go func() {
		for _, job := range processJobs {
			j := job
			jobs <- j
		}
	}()

	log.Println("Waiting on threads to finish reading all your images and media...")
	wg.Wait()
	log.Println(logger.TimeTrack(startEntireProcess, "Completed in"))
}
