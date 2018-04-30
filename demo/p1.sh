#!/bin/bash
CMD='kubectl patch hpa.v2beta1.autoscaling test --patch "$(cat patch1.yaml)"'
echo $CMD
eval $CMD
