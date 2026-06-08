-- 0027_project_members.down.sql — reverse of the up migration.
--
-- The owner promotion is reverted (owner → admin). The reviewer/viewer
-- fold is NOT losslessly reversible: the original reviewer/viewer
-- distinction was unused (no code branched on it — audited in sty_0ce67ed2),
-- so folded rows remain member, which is the documented, acceptable
-- behaviour of the down path.

UPDATE workspace_members SET role = 'admin' WHERE role = 'owner';

DROP TABLE IF EXISTS project_members;
