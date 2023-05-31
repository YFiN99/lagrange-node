#!/bin/sh

set -e

gen() {
    local package=$1

    abigen --bin bin/${package}.bin --abi bin/${package}.abi --pkg=${package} --out=${package}/${package}.go
}

gen lagrange