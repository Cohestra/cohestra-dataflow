#!/bin/bash
# one.sh <model-name>: run a single models.txt entry through bakeoff.sh
cd "$(dirname "$0")/.."
grep "^$1|" scripts/models.txt > "scripts/one-$$.txt" && scripts/bakeoff.sh "scripts/one-$$.txt"; rm -f "scripts/one-$$.txt"
