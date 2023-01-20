#!/usr/bin/env bash
set -e -o pipefail -u

cd -- "$(dirname -- "$0")"

rm -r ../_vendor/trace || true
mkdir ../_vendor/trace

tar -c -C "$(go list -f '{{.Dir}}' internal/trace)" -- \
    $(go list -f '{{join .GoFiles " "}}' internal/trace) | \
    tar -x -C ../_vendor/trace
