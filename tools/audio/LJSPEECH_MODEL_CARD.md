# Voice used for the countdown audio

Copied from https://huggingface.co/rhasspy/piper-voices/raw/main/en/en_US/ljspeech/high/MODEL_CARD on 2026-11-13.
The voice model itself is not part of this repository; only audio generated with it is.

# Model card for ljspeech (high)

* Language: en_US (English, United States)
* Speakers: 1
* Quality: high
* Samplerate: 22,050Hz

## Dataset

* URL: https://keithito.com/LJ-Speech-Dataset/
* License: public domain

## Training

See: https://brycebeattie.com/files/tts/

US English female voice. Single speaker. Trained from scratch for 1000 epochs on medium quality settings using the LJ Speech dataset. I reencoded the recordings to a bit rate of 22500 Hz so it would match other voices released for Piper TTS.
