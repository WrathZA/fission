#!/bin/bash

#
# Create two functions, make sure their internal http triggers invoke
# them correctly.
#

set -euo pipefail

ROOT=$(dirname $0)/../..

log "Pre-test cleanup"
fission env delete --name nodejs || true

log "Creating nodejs env"
fission env create --name nodejs --image fission/node-env
trap "fission env delete --name nodejs" EXIT

log "Writing functions"
f1=f1-$(date +%s)
f2=f2-$(date +%s)
log $f1 $f2

for f in $f1 $f2
do
    echo "module.exports = function(context, callback) { callback(200, \"$f\n\"); }" > $f.js
done

log "Creating functions"
for f in $f1 $f2
do
    fission fn create --name $f --env nodejs --code $f.js
    trap "fission fn delete --name $f" EXIT
done

log "Waiting for router to catch up"
sleep 2

log "Testing internal routes"
for f in $f1 $f2
do
    response=$(curl http://$FISSION_ROUTER/fission-function/$f)
    echo $response | grep $f
done

log "All done."
