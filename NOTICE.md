# Notice

Galaxia FMS is a derivative of [Cheesy Arena](https://github.com/Team254/cheesy-arena), the field management
system written by Team 254 (Patrick Fairbank and contributors).

- Cheesy Arena is copyright (c) 2014 Team 254 and is used under the terms in [LICENSE](LICENSE). That file
  and every Team 254 copyright header in the source are kept unchanged.
- The licence permits use and modification for testing, scrimmages, off-season events and evaluation, and
  restricts redistribution of modifications. Read it before reusing this code.
- Galaxia FMS is not affiliated with or endorsed by Team 254 or by FIRST.

## What Galaxia changes

Galaxia keeps Cheesy Arena's match control, driver station communication, network configuration, scheduling,
playoff and publishing code, and replaces the parts that are written for one specific game with a
data-driven game definition engine, a device framework for field electronics, practice modes, an event bus
and API, and new displays. The plan and its progress are in [docs/PLAN.md](docs/PLAN.md).

The history of this repository contains the complete upstream history. Changes made for Galaxia begin after
upstream commit `248d9de` (2026-10-19).
