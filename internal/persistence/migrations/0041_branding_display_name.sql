UPDATE payment_seller_profiles
SET seller_of_record = 'Billing Simulator',
	updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE seller_of_record = 'AWS Billing Simulator';

UPDATE invoice_documents
SET seller_of_record = 'Billing Simulator',
	updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE seller_of_record = 'AWS Billing Simulator';

PRAGMA user_version = 41;
