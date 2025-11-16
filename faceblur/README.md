# Face Blur Script

A bash script to blur faces in images and videos using the [deface](https://github.com/ORB-HD/deface) library.

## Installation

The script will automatically install `deface` if it's not already installed. You can also install it manually:

```bash
pip install deface
```

## Usage

### Basic Usage

Blur faces in an image (output defaults to `input_blurred.jpg`):

```bash
./blur_faces.sh input.jpg
```

Blur faces in a video (output defaults to `input_blurred.mp4`):

```bash
./blur_faces.sh input.mp4
```

Specify a custom output file:

```bash
./blur_faces.sh input.jpg output.jpg
```

### Options

- `output` (optional): Path to save the processed file. If not provided, defaults to the input filename with `_blurred` added before the extension (e.g., `photo.jpg` → `photo_blurred.jpg`).

- `--mode`: Anonymization filter mode (default: `blur`)

  - `blur`: Blur faces
  - `solid`: Replace faces with solid boxes
  - `mosaic`: Apply mosaic effect
  - `img`: Replace with an image
  - `none`: No replacement (for testing detection)

- `--threshold`: Detection threshold (0.0 to 1.0, default: 0.5)

  - Lower values detect more faces (more sensitive)
  - Higher values detect fewer faces (less sensitive)

- `--keep-audio`: Keep audio track in video output (default: audio is discarded)

- `--backend`: Backend to use for face detection (default: auto)

  - `auto`: Automatically select the best available backend
  - `onnxrt`: Use ONNX Runtime backend
  - `opencv`: Use OpenCV backend

- `--scale`: Downscale frames for network inference (format: WxH, e.g., 640x360)
  - Improves processing performance by analyzing smaller frames
  - Output video maintains original resolution
  - May reduce detection accuracy for very small faces
  - Example: `--scale 640x360` for faster processing

### Examples

Use solid boxes instead of blur:

```bash
./blur_faces.sh input.jpg --mode solid
```

Use mosaic effect:

```bash
./blur_faces.sh input.mp4 --mode mosaic
```

Adjust detection sensitivity:

```bash
./blur_faces.sh input.jpg --threshold 0.3
```

Keep audio in video output:

```bash
./blur_faces.sh input.mp4 --keep-audio
```

Use specific backend:

```bash
./blur_faces.sh input.mp4 --backend onnxrt
```

Downscale for faster processing:

```bash
./blur_faces.sh input.mp4 --scale 640x360
```

Combine options:

```bash
./blur_faces.sh input.jpg custom_output.jpg --mode solid --threshold 0.3
```

## Supported Formats

- **Images**: JPEG, PNG, and other common image formats
- **Videos**: MP4, AVI, MOV, and other common video formats

## Notes

- The script automatically installs `deface` if it's not already installed
- The script automatically creates output directories if they don't exist
- For videos, audio tracks are discarded by default. Use `--keep-audio` to preserve audio
- Processing time depends on file size and resolution
