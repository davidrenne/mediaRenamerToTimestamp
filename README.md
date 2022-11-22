# mediaRenamerToTimestamp

Tool to re-name recursively media files (all image types and MP4 and MOV files).  e.g. 1997-05-01 12.15.33.jpg

## Usage

To start, clone the repo:

```bash
go install https://github.com/davidrenne/mediaRenamerToTimestamp

mediaRenamerToTimestamp "Y:\YourFiles\"
```

Or pass a custom timestamp format

```bash
mediaRenamerToTimestamp "Y:\YourFiles\" "Any format using these date times https://www.geeksforgeeks.org/time-formatting-in-golang/ such as RFC850 Monday, 02-Jan-06 15:04:05 MST"
```

## Warning

This tool will rename your files if the exif and meta data is parsed correctly
