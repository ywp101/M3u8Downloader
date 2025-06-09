# M3u8 Downloader

## Overview
This app is based on the Golang design to download videos, not only to download m3u8, but also to download small movies directly.

## Features
- Concurrent downloading.
- Support for multiple video sources.
- Support nested playlists.
- Modular design for easy extension.

## Support Site
- https://jable.tv/
- https://hohoj.tv/
- https://missav.ai/
- https://memojav.com/
- https://avtoday.io/
- https://f15.bzraizy.cc/

## Project Structure
- `mapreduce.go`: Implements a generic MapReduce framework for concurrent task processing.
- `dl_master.go`: Handles video metadata fetching and downloading.
- `ts_writer.go`: Manages writing TS video segments to files.

## Installation

1. Clone the repository:
   ```bash
   git clone <repository-url>
   cd <repository-folder>
   go mod tidy
   go build
    ```
2. Run the Downloader
Add video URLs to the DLMaster instance and start the download process.
   ```
   m3u8downloader http://xxxxx.m3u8
   or
   m3u8downloader file.list
   ```

file.list格式
   ```
   http://xxxxx.m3u8;fileName
   https://jable.tv/videos/nsfs-376/
   ```