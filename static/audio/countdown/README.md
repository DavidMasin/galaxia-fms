# Countdown sounds

The match-start countdown: "Drivers, behind the lines", "Three", "Two", "One", "Go!", and a riser that swells into
"Go!". They are original to Galaxia FMS and are made by [tools/audio/make_countdown_audio.py](../../../tools/audio/make_countdown_audio.py):

- The voice was generated offline with Piper using the `en_US-ljspeech-high` voice, whose dataset and model are
  public domain ([model card](../../../tools/audio/LJSPEECH_MODEL_CARD.md)).
- The impacts, shockwave and riser are synthesised from noise and sine waves by the same script.

Timing matters: each beat file starts exactly on its beat, and `riser.wav` is exactly 3 seconds long and ends on
"Go!". [countdown.json](countdown.json) describes this for the displays. Replacements uploaded from the settings
page should follow the same rule, or set their own offsets.
