#!/usr/bin/env bash

# Example script for setting wihheld sports events to cancelled and set their
# newsvalue to 1.

set -euo pipefail

export ENV=prod

elephant login

uuids=$(
  elephant search <<'EOF'
{
  "document_type": "core/event",
  "query": {
    "bool": {
      "must": [
        {
          "term": {
            "field": "document.rel.section.uuid",
            "value": "0730efa9-43f2-468d-979a-aaffc74d7582"
          }
        },
        {
          "range": {
            "field": "heads.withheld.id",
            "gte": "1"
          }
        },
        {
          "range": {
            "field": "document.meta.core_event.data.start",
            "gte": "2025-08-01"
          }
        },
        {
          "range": {
            "field": "modified",
            "lt": "2025-06-01"
          }
        }
      ],
      "must_not": [
        {
          "bool": {
            "must": [
              {
                "term": {
                  "field": "workflow_state",
                  "value": "cancelled"
                }
              },
              {
                "term": {
                  "field": "document.meta.core_newsvalue.value",
                  "value": "1"
                }
              }
            ]
          }
        }
      ]
    }
  }
}
EOF
)

echo "This will update the following documents:"

for id in $uuids; do
    echo $id
done

echo

echo "Number of documents: $(wc -l <<< $uuids)"

read -p "Are you sure? [y/n]: " -n 1 -r
echo

if [[ $REPLY =~ ^[Yy]$ ]]
then
    for id in $uuids; do
        echo "Updating $id"

        elephant doc-download -uuid $id \
            | jq '(.meta[] | select(.type == "core/newsvalue") | .value) = "1"' \
            | elephant doc-upload

        elephant set-status \
                 -uuid $id -status cancelled \
                 -meta reason=import-cleanup

        echo ""
    done
fi


