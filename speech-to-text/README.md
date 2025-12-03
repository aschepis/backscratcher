# 🎙️ WhisperX Transcript & Diarization Tool

This guide shows how to **generate a clean, readable transcript with timestamps and speaker labels** from any video or audio file — all running locally inside Docker.

**NOTE: All processing runs locally, and no data leaves your machine.**

---

## 🚀 Quick Start

### 0. Prerequisites

- Docker
- jq
- Hugging Face token (for diarization models)
  - [https://huggingface.co/settings/tokens](https://huggingface.co/settings/tokens)
  - Generate a **read token** at: [https://huggingface.co/settings/tokens](https://huggingface.co/settings/tokens)
  - Pass it to Docker with `-e HF_TOKEN=...` and `--hf_token ...`
  - You only need to do this once per account.

### 1. Prepare your video/audio file

Place your media (e.g. `vid1.mp4`) in a working folder:

```bash
mkdir ~/whisperx_test
cp /path/to/vid1.mp4 ~/whisperx_test
cd ~/whisperx_test
```

### 2. Run WhisperX in Docker

If you don’t already have it:

```bash
docker pull ghcr.io/jim60105/docker-whisperx:latest
```

Then transcribe and diarize your video:

```bash
docker run -it --rm \
  -e HF_TOKEN=$HF_TOKEN \
  -v ".:/app" ghcr.io/jim60105/whisperx:base-en \
  -- --print_progress=True --compute_type=float32 \
  --diarize --log-level=debug --hf_token $HF_TOKEN \
  ./vid1.mp4
```

📝 After processing, you’ll see output files like:

```
vid1.mp4.json
vid1.mp4.srt
vid1.mp4.transcription.txt
```

---

### 3. Convert JSON to a Clean Transcript

Use `jq` (preinstalled on most macOS/Linux systems):

```bash
jq -r '.segments[] | "\(.start)–\(.end)  \(.speaker // "Unknown"): \(.text)"' vid1.mp4.json \
  > vid1.mp4.flat.txt
```

You’ll get a simple, human-readable text file like:

```
0–27.12  SPEAKER_00: Oh perfect! So you would want to have basically a timeline...
27.12–35.44  SPEAKER_01: Get out of your vehicle.
35.44–45.18  SPEAKER_02: Leave him alone!
```

---

## 🧭 Overview

This workflow uses **[WhisperX](https://github.com/m-bain/whisperX)** — an enhanced version of OpenAI’s Whisper — with **speaker diarization** from the [pyannote.audio](https://github.com/pyannote/pyannote-audio) pipeline.
It performs three main steps:

1. **Automatic Speech Recognition (ASR)** – Converts speech to text.
2. **Alignment** – Synchronizes each word to its timestamp.
3. **Diarization** – Identifies _who_ spoke each segment.

---

## 🔐 About the Hugging Face Token

Pyannote’s diarization models (`pyannote/speaker-diarization-3.1` and `pyannote/segmentation-3.0`) are gated.
You must:

1. Create a **free Hugging Face account**: [https://huggingface.co](https://huggingface.co)
2. Accept the model licenses:

   - [https://huggingface.co/pyannote/speaker-diarization-3.1](https://huggingface.co/pyannote/speaker-diarization-3.1)
   - [https://huggingface.co/pyannote/segmentation-3.0](https://huggingface.co/pyannote/segmentation-3.0)

3. Generate a **read token** at: [https://huggingface.co/settings/tokens](https://huggingface.co/settings/tokens)
4. Pass it to Docker with `-e HF_TOKEN=...` and `--hf_token ...`

You only need to do this once per account.

---

## 🧠 Notes and Tips

- Works on **macOS, Linux, or Windows** (via Docker Desktop).
- The CPU pipeline is slow but fully offline and privacy-safe.
- The `.json` output is the richest format — it contains start/end times, speakers, and full text.
- If you prefer timestamps in minutes/seconds instead of raw seconds, you can tweak the `jq` command:

  ```bash
  jq -r '.segments[] |
    def fmt(t): (t/60 | floor | tostring) + ":" + ((t%60 | floor | tostring) | lpad(2; "0"));
    "\(fmt(.start))–\(fmt(.end))  \(.speaker // "Unknown"): \(.text)"' vid1.mp4.json
  ```

---

## ⚙️ Optional Enhancements

### Batch convert all JSON files

```bash
for f in *.json; do
  jq -r '.segments[] | "\(.start)–\(.end)  \(.speaker // "Unknown"): \(.text)"' "$f" \
    > "${f%.json}.flat.txt"
done
```

### Export to CSV

```bash
jq -r '.segments[] | [.start, .end, (.speaker // "Unknown"), (.text | gsub("\n"; " "))] | @csv' vid1.mp4.json > vid1.mp4.csv
```

### Burn subtitles into the video (optional)

```bash
ffmpeg -i vid1.mp4 -vf subtitles=vid1.mp4.srt -c:a copy vid1_subtitled.mp4
```

---

## 📦 Requirements

- **Docker** (for WhisperX)
- **jq** (for JSON post-processing)
- Optional: **ffmpeg** (to embed subtitles)
- A **Hugging Face token** (for diarization models)

---

## 🧩 License & Attribution

This workflow builds on open-source components:

- [WhisperX](https://github.com/m-bain/whisperX) (MIT License)
- [pyannote.audio](https://github.com/pyannote/pyannote-audio)
- [OpenAI Whisper](https://github.com/openai/whisper)
- [jim60105/docker-whisperx](https://github.com/jim60105/docker-whisperX) Docker image

All processing runs **locally**, and no data leaves your machine.
