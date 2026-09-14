-- 0024_rls_policies: structural tenant boundary, step 1 of 2 (Phase 0 task 3).
--
-- Defines a fail-closed tenant policy on every ferp_* table carrying
-- entity_id: when app.entity_id is set, only that entity's rows are visible;
-- when unset, NO rows are visible (NULL comparison denies).
--
-- NOT YET ENFORCED: ENABLE/FORCE ROW LEVEL SECURITY is deliberately absent.
-- Enabling today would deny every direct-pool query, because SET LOCAL is
-- transaction-scoped and the app has no request-scoped transaction yet.
-- Enforcement lands with Phase 1 service completion: a follow-up migration
-- runs ALTER TABLE ... ENABLE + FORCE ROW LEVEL SECURITY per table below,
-- safe only once every request path runs inside platform.TxEntity (which
-- sets app.entity_id first). Services converted so far: pos checkout,
-- hr payout, manufacturing produce.
--
-- Out of scope (no entity_id column by design):
--   ferp_entities                the tenants table itself (global by nature)
--   ferp_outbox                  global dispatch queue (entity rides in payload)
--   ferp_schema_migrations       created in code by platform.Migrate, not a migration
--   ferp_sessions                auth sessions, keyed by login token
--   ferp_group_members           membership links, guarded via parent group
--   ferp_category_links          guarded via parent category
--   ferp_doc_lines, ferp_supplier_doc_lines, ferp_entry_lines,
--   ferp_payment_allocations, ferp_credit_allocations,
--   ferp_supplier_allocations    child rows, cascade-guarded via parent docs
--   ferp_stock_levels            product/warehouse keyed, guarded via product checks
--   ferp_job_runs                run rows, guarded via parent job
-- Superusers bypass RLS; CI connects as the non-superuser forgeerp role.

CREATE POLICY tenant_isolation ON ferp_accounts
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_applications
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_articles
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_assets
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_bank_accounts
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_bank_transactions
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_bom_lines
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_boms
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_bookings
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_categories
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_config
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_contacts
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_doc_counters
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_documents
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_donations
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_entries
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_events
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_expense_lines
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_expense_reports
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_files
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_fiscal_years
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_fx_rates
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_groups
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_interventions
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_jobs
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_journals
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_leave_requests
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_loans
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_lots
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_mailboxes
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_member_types
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_members
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_mos
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_notify_outbox
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_notify_templates
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_org_events
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_organizations
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_payment_attempts
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_payments
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_pos_sales
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_pos_sessions
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_pos_terminals
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_positions
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_product_variants
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_products
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_project_tasks
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_projects
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_registrations
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_resources
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_rights
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_salaries
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_sepa_batches
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_service_contracts
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_share_tokens
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_stock_movements
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_subscriptions
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_supplier_docs
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_supplier_payments
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_supplier_prices
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_survey_options
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_survey_questions
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_survey_votes
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_surveys
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_ticket_messages
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_tickets
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_time_entries
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_users
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_warehouses
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_webhooks
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
