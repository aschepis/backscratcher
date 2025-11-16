#!/bin/bash
# Script to blur faces in images and videos using the deface library.

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Default values
MODE="blur"
THRESHOLD="0.5"
OUTPUT=""
KEEP_AUDIO=false
BACKEND=""
SCALE=""

# Function to check if deface is installed
check_deface_installed() {
    if command -v deface &> /dev/null; then
        return 0
    else
        return 1
    fi
}

# Function to install deface
install_deface() {
    echo -e "${YELLOW}deface is not installed. Installing...${NC}"
    if command -v pip3 &> /dev/null; then
        pip3 install deface
    elif command -v pip &> /dev/null; then
        pip install deface
    else
        echo -e "${RED}Error: pip is not installed. Please install Python and pip first.${NC}" >&2
        exit 1
    fi

    if ! check_deface_installed; then
        echo -e "${RED}Error: Failed to install deface. Please install it manually with: pip install deface${NC}" >&2
        exit 1
    fi

    echo -e "${GREEN}deface installed successfully!${NC}"
}

# Function to generate output path
generate_output_path() {
    local input_path="$1"
    local dirname=$(dirname "$input_path")
    local basename=$(basename "$input_path")
    local filename="${basename%.*}"
    local extension="${basename##*.}"

    if [ "$dirname" = "." ] || [ -z "$dirname" ]; then
        echo "${filename}_blurred.${extension}"
    else
        echo "${dirname}/${filename}_blurred.${extension}"
    fi
}

# Function to show usage
show_usage() {
    cat << EOF
Usage: $0 INPUT [OUTPUT] [OPTIONS]

Blur faces in images and videos using deface.

Arguments:
  INPUT                 Path to the input image or video file (required)
  OUTPUT                Path to save the processed file (optional, defaults to INPUT_blurred.ext)

Options:
  --mode MODE           Anonymization filter mode: blur, solid, none, img, mosaic (default: blur)
  --threshold VALUE     Detection threshold 0.0 to 1.0 (default: 0.5)
                        Lower values detect more faces (more sensitive)
  --keep-audio          Keep audio track in video output (default: audio is discarded)
  --backend BACKEND     Backend to use: auto, onnxrt, opencv (default: auto)
  --scale WxH           Downscale frames for network inference (format: WxH, e.g., 640x360)
                        Improves performance but may reduce detection accuracy for small faces

Examples:
  # Blur faces in an image (output defaults to input_blurred.jpg)
  $0 input.jpg

  # Blur faces in a video (output defaults to input_blurred.mp4)
  $0 input.mp4

  # Specify custom output file
  $0 input.jpg output.jpg

  # Use solid boxes instead of blur
  $0 input.jpg --mode solid

  # Use mosaic effect
  $0 input.mp4 --mode mosaic

  # Adjust detection threshold (lower = more sensitive)
  $0 input.jpg --threshold 0.3

  # Keep audio in video output
  $0 input.mp4 --keep-audio

  # Use specific backend
  $0 input.mp4 --backend onnxrt

  # Downscale for faster processing
  $0 input.mp4 --scale 640x360

EOF
}

# Check if no arguments provided
if [ $# -eq 0 ]; then
    echo -e "${RED}Error: INPUT file is required${NC}" >&2
    echo ""
    show_usage
    exit 1
fi

# Parse arguments
INPUT=""
while [[ $# -gt 0 ]]; do
    case $1 in
        --mode)
            if [ -z "$2" ] || [[ "$2" == -* ]]; then
                echo -e "${YELLOW}Warning: --mode requires a value. Using default: blur${NC}" >&2
                MODE="blur"
                shift
            else
                MODE="$2"
                shift 2
            fi
            ;;
        --threshold)
            if [ -z "$2" ] || [[ "$2" == -* ]]; then
                echo -e "${YELLOW}Warning: --threshold requires a value. Using default: 0.5${NC}" >&2
                THRESHOLD="0.5"
                shift
            else
                THRESHOLD="$2"
                shift 2
            fi
            ;;
        --keep-audio)
            KEEP_AUDIO=true
            shift
            ;;
        --backend)
            if [ -z "$2" ] || [[ "$2" == -* ]]; then
                echo -e "${YELLOW}Warning: --backend requires a value. Using default: auto${NC}" >&2
                BACKEND="auto"
                shift
            else
                BACKEND="$2"
                shift 2
            fi
            ;;
        --scale)
            if [ -z "$2" ] || [[ "$2" == -* ]]; then
                echo -e "${YELLOW}Warning: --scale requires a value in WxH format (e.g., 640x360)${NC}" >&2
                SCALE=""
                shift
            else
                SCALE="$2"
                shift 2
            fi
            ;;
        --help|-h)
            show_usage
            exit 0
            ;;
        -*)
            echo -e "${RED}Error: Unknown option $1${NC}" >&2
            echo ""
            show_usage
            exit 1
            ;;
        *)
            if [ -z "$INPUT" ]; then
                INPUT="$1"
            elif [ -z "$OUTPUT" ]; then
                OUTPUT="$1"
            else
                echo -e "${RED}Error: Too many arguments${NC}" >&2
                echo ""
                show_usage
                exit 1
            fi
            shift
            ;;
    esac
done

# Check if input is provided
if [ -z "$INPUT" ]; then
    echo -e "${RED}Error: INPUT file is required${NC}" >&2
    echo ""
    show_usage
    exit 1
fi

# Check if input file exists
if [ ! -f "$INPUT" ]; then
    echo -e "${RED}Error: Input file not found: $INPUT${NC}" >&2
    exit 1
fi

# Generate output path if not provided
if [ -z "$OUTPUT" ]; then
    OUTPUT=$(generate_output_path "$INPUT")
    echo -e "${YELLOW}Output not specified, using default: $OUTPUT${NC}"
fi

# Validate threshold
if ! awk -v t="$THRESHOLD" 'BEGIN {if (t >= 0.0 && t <= 1.0) exit 0; exit 1}'; then
    echo -e "${RED}Error: threshold must be between 0.0 and 1.0${NC}" >&2
    exit 1
fi

# Validate mode
case "$MODE" in
    blur|solid|none|img|mosaic)
        ;;
    *)
        echo -e "${RED}Error: Invalid mode '$MODE'. Must be one of: blur, solid, none, img, mosaic${NC}" >&2
        exit 1
        ;;
esac

# Validate backend if provided
if [ -n "$BACKEND" ]; then
    case "$BACKEND" in
        auto|onnxrt|opencv)
            ;;
        *)
            echo -e "${RED}Error: Invalid backend '$BACKEND'. Must be one of: auto, onnxrt, opencv${NC}" >&2
            exit 1
            ;;
    esac
fi

# Validate scale format if provided
if [ -n "$SCALE" ]; then
    if ! [[ "$SCALE" =~ ^[0-9]+x[0-9]+$ ]]; then
        echo -e "${RED}Error: Invalid scale format '$SCALE'. Must be in WxH format (e.g., 640x360)${NC}" >&2
        exit 1
    fi
fi

# Ensure deface is installed
if ! check_deface_installed; then
    install_deface
fi

# Create output directory if it doesn't exist
OUTPUT_DIR=$(dirname "$OUTPUT")
if [ -n "$OUTPUT_DIR" ] && [ "$OUTPUT_DIR" != "." ] && [ ! -d "$OUTPUT_DIR" ]; then
    mkdir -p "$OUTPUT_DIR"
fi

# Run deface
echo -e "${GREEN}Processing: $INPUT${NC}"
echo -e "${GREEN}Using mode: $MODE, threshold: $THRESHOLD${NC}"
if [ -n "$BACKEND" ]; then
    echo -e "${GREEN}Using backend: $BACKEND${NC}"
fi
if [ -n "$SCALE" ]; then
    echo -e "${GREEN}Downscaling frames to: $SCALE${NC}"
fi

# Build deface command arguments
DEFACE_ARGS=("$INPUT" --output "$OUTPUT" --replacewith "$MODE" --thresh "$THRESHOLD")

if [ "$KEEP_AUDIO" = true ]; then
    echo -e "${GREEN}Keeping audio track${NC}"
    DEFACE_ARGS+=("--keep-audio")
fi

if [ -n "$BACKEND" ]; then
    DEFACE_ARGS+=("--backend" "$BACKEND")
fi

if [ -n "$SCALE" ]; then
    DEFACE_ARGS+=("--scale" "$SCALE")
fi

# Execute deface command
deface "${DEFACE_ARGS[@]}"

if [ $? -eq 0 ]; then
    echo -e "${GREEN}Successfully processed: $INPUT${NC}"
    echo -e "${GREEN}Output saved to: $OUTPUT${NC}"
else
    echo -e "${RED}Error processing file${NC}" >&2
    exit 1
fi

