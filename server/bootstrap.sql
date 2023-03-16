create database austender;
use austender;
CREATE TABLE contracts (
 CNID String,
 Agency String,
 PublishDate DateTime,
 Category String,
 ContractValue Decimal64(3),
 ContractPeriod String,
 ATMID String,
 SONID String,
 SupplierName String
) 
ENGINE = MergeTree()
PARTITION BY toYYYYMMDD(PublishDate)
ORDER BY (PublishDate);
INSERT INTO contracts VALUES (
    'CN3717136',
    'Office of the Australian Information Commissioner',
    '2020-09-15 00:01:01',
    'Information technology consultation services',
    768856.24,
    '17-Aug-2020 to 30-Jun-2022',
    'SON3622041',
    'PricewaterhouseCoopers'
);