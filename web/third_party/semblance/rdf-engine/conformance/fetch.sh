#!/bin/sh
# Fetch the W3C rdf-tests suites the conformance runner uses, pinned to one commit.
set -e
COMMIT=bfefa770267ec0e5b6e45764bbca4ee43789ba72
DIR="$(cd "$(dirname "$0")" && pwd)/rdf-tests"
if [ ! -d "$DIR/.git" ]; then
  git clone --quiet --filter=blob:none --no-checkout https://github.com/w3c/rdf-tests.git "$DIR"
fi
cd "$DIR"
git sparse-checkout set sparql/sparql10 sparql/sparql11 sparql/sparql12 rdf/rdf11 rdf/rdf12
git fetch --quiet --depth 1 origin "$COMMIT"
git checkout --quiet "$COMMIT"
echo "rdf-tests at $COMMIT in $DIR"
