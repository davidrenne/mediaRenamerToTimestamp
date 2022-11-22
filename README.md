# mediaRenamerToTimestamp

Tool to re-name recursively media files (all image types and MP4 and MOV files).  e.g. `1997-05-01 12.15.33.jpg` so that they sort properly on a normal filesystem (mac/windows/linux)

## Reasoning

I am a huge dropbox fan of how they sync multiple phone files and digital camera cards and rename to this format.  I use the free 2GB account to sync my wife's phone and mine and 1 desktop as a staging area to sync to google photos and my folder based storage.  But I wanted my own way to rename files so I wrote this out of necessity to clean up some of my media collection of family photos.

Once you start having files renamed to their date taken, you will never like seeing a mix of IMG_1234.JPG and stuff out of order of the order in which things happened IRL.

## Usage

To start, clone the repo:

```bash
go install github.com/davidrenne/mediaRenamerToTimestamp@02902b80b3750b2c2f7ed65ecde8f6d08c74ed20

mediaRenamerToTimestamp "Y:\YourFiles\"
```

Or pass a custom timestamp format

```bash
mediaRenamerToTimestamp "Y:\YourFiles\" "Any format using these date times https://www.geeksforgeeks.org/time-formatting-in-golang/ such as RFC850 Monday, 02-Jan-06 15:04:05 MST"
```

## Warning

This tool will rename your files if the exif and meta data is parsed correctly
