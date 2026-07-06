# Integration run C on Lima nodes

Prerequisites: lima-essaim-01/02 are up; run B has already been executed on them (there is
`/var/lib/rucher/agent-status.json`); `rucher` built for linux and present on the nodes;
the Mac has `./nodes/lima-essaim-0{1,2}/configuration.yml`.

The C part is validated over the existing lima-SSH (no overlay needed):

1. `rucher ops ruches status` (from the directory with `./nodes`) → a NODE/ADDRESS/REACHABLE/REVISION/
   APPLIED/REMOVED/ERRORS table for both nodes; revisions match what the agent applied.
   For an unreachable node (stop lima-essaim-02) the row shows REACHABLE=no, and the table doesn't break.
2. `rucher ops ruches status --live lima-essaim-01` → plus a `rucher node cadre status` block with live ActiveState/SubState.
3. `rucher ops ruches join lima-essaim-01 --address 100.64.0.1` → in
   `./nodes/lima-essaim-01/configuration.yml` a `network: {address: 100.64.0.1}` appears,
   other keys/comments left in place. A repeated `ops ruches join` with a different address updates the value.
