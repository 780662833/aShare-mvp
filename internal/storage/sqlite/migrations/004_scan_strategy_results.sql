ALTER TABLE scan_run_items
    ADD COLUMN strategy_results TEXT NOT NULL DEFAULT '[]';
