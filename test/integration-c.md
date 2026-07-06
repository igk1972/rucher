# Integration run C on Lima nodes

Prerequisites: lima-essaim-01/02 are up; run B has already been executed on them (there is
`/var/lib/podman-essaim/agent-status.json`); `pecm` built for linux/arm64 and present on the nodes;
the Mac has `./hosts/lima-essaim-0{1,2}/configuration.yml`.

The C part is validated over the existing lima-SSH (no overlay needed):

1. `pecm hosts status` (from the directory with `./hosts`) → a HOST/ADDRESS/REACHABLE/REVISION/
   APPLIED/REMOVED/ERRORS table for both nodes; revisions match what the agent applied.
   For an unreachable node (stop lima-essaim-02) the row shows REACHABLE=no, and the table doesn't break.
2. `pecm hosts status --live lima-essaim-01` → plus a `pecm status` block with live ActiveState/SubState.
3. `pecm net join lima-essaim-01 --address 100.64.0.1` → in
   `./hosts/lima-essaim-01/configuration.yml` a `network: {address: 100.64.0.1}` appears,
   other keys/comments left in place. A repeated `net join` with a different address updates the value.
