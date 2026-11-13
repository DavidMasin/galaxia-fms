// Output commands waiting for their "at" time. Plain C++, shared rules with the other SDKs (sdk/README.md):
// a new command replaces every pending command due at or after its own time, so an immediate command cancels
// everything pending, while commands sent ahead for successive moments queue up. When the queue is full the
// earliest pending command is dropped.
// MIT licence, copyright Galaxia FMS contributors.

#ifndef GALAXIA_SCHEDULE_H
#define GALAXIA_SCHEDULE_H

#include <stddef.h>
#include <stdint.h>

namespace galaxia {

template <typename T, size_t N>
class Schedule {
 public:
  // Returns true if the command is due now (the caller applies it); otherwise it is kept.
  bool offer(bool hasAt, int64_t at, bool clockValid, int64_t now, const T& command) {
    if (!hasAt || !clockValid || at <= now) {
      count_ = 0;
      return true;
    }
    size_t keep = 0;
    while (keep < count_ && at_[keep] < at) keep++;
    count_ = keep;
    if (count_ == N) {
      for (size_t i = 1; i < N; i++) {
        at_[i - 1] = at_[i];
        commands_[i - 1] = commands_[i];
      }
      count_--;
      dropped_++;
    }
    at_[count_] = at;
    commands_[count_] = command;
    count_++;
    return false;
  }

  // Moves the next due command into `out`. Call repeatedly until it returns false.
  bool popDue(bool clockValid, int64_t now, T& out, int64_t& at) {
    if (count_ == 0) return false;
    if (clockValid && at_[0] > now) return false;
    out = commands_[0];
    at = at_[0];
    for (size_t i = 1; i < count_; i++) {
      at_[i - 1] = at_[i];
      commands_[i - 1] = commands_[i];
    }
    count_--;
    return true;
  }

  void clear() { count_ = 0; }
  size_t size() const { return count_; }
  uint32_t dropped() const { return dropped_; }

 private:
  int64_t at_[N] = {};
  T commands_[N];
  size_t count_ = 0;
  uint32_t dropped_ = 0;
};

// Estimates the FMS clock from field/clock messages. Each message gives one sample of (FMS time - local time).
// Network delay can only make a sample too small, so the estimate is the largest of the last eight. A sample more
// than a second away from the estimate is ignored, unless three arrive in a row: then the FMS clock really jumped.
class ClockEstimator {
 public:
  void sample(int64_t offset) {
    if (valid_ && (offset - offset_ > kJumpMs || offset_ - offset > kJumpMs)) {
      outliers_[outlierCount_++] = offset;
      if (outlierCount_ < kOutliers) return;
      count_ = 0;
      next_ = 0;
      for (uint8_t i = 0; i < kOutliers; i++) push(outliers_[i]);
      outlierCount_ = 0;
    } else {
      outlierCount_ = 0;
      push(offset);
    }
    int64_t best = samples_[0];
    for (uint8_t i = 1; i < count_; i++) {
      if (samples_[i] > best) best = samples_[i];
    }
    offset_ = best;
    valid_ = true;
  }

  bool valid() const { return valid_; }
  int64_t offset() const { return offset_; }

 private:
  static constexpr uint8_t kWindow = 8;
  static constexpr uint8_t kOutliers = 3;
  static constexpr int64_t kJumpMs = 1000;

  void push(int64_t value) {
    samples_[next_] = value;
    next_ = (next_ + 1) % kWindow;
    if (count_ < kWindow) count_++;
  }

  int64_t samples_[kWindow] = {};
  uint8_t count_ = 0;
  uint8_t next_ = 0;
  int64_t outliers_[kOutliers] = {};
  uint8_t outlierCount_ = 0;
  bool valid_ = false;
  int64_t offset_ = 0;
};

}  // namespace galaxia

#endif
