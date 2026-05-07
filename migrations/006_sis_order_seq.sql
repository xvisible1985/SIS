-- migrations/006_sis_order_seq.sql
-- Persistent counter for SiS order link IDs (SiS-1, SiS-2, ...)

CREATE SEQUENCE IF NOT EXISTS sis_order_seq START 1 INCREMENT 1;
