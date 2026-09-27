/**
 * Sample ISO 20022 camt documents, shared by the reader tests and the
 * end-to-end parse test.
 *
 * Every element sits in the camt default namespace, which is what real files do
 * and what makes a namespace-blind lookup (local name only) the correct one.
 */

// A camt.053 bank statement: one account, one period, three booked entries.
// Note the counterparties -- a money-out entry names its creditor (Cdtr) and a
// money-in entry names its debtor (Dbtr), which is the party actually involved.
export const CAMT_053_BANK = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <BkToCstmrStmt>
    <GrpHdr><MsgId>MSG-1</MsgId><CreDtTm>2026-05-31T12:00:00</CreDtTm></GrpHdr>
    <Stmt>
      <Id>STMT-0001</Id>
      <ElctrncSeqNb>1</ElctrncSeqNb>
      <Acct>
        <Id><IBAN>IN40100234567890</IBAN></Id>
        <Ccy>INR</Ccy>
        <Nm>Suyash Mittal</Nm>
        <Svcr><FinInstnId><BIC>HDFC0001</BIC></FinInstnId></Svcr>
      </Acct>
      <Ntry>
        <Amt Ccy="INR">1250.50</Amt>
        <CdtDbtInd>DBIT</CdtDbtInd>
        <Sts>BOOK</Sts>
        <BookgDt><Dt>2026-05-18</Dt></BookgDt>
        <ValDt><Dt>2026-05-18</Dt></ValDt>
        <NtryDtls>
          <TxDtls>
            <Refs><EndToEndId>E2E-1</EndToEndId></Refs>
            <Amt Ccy="INR">1250.50</Amt>
            <CdtDbtInd>DBIT</CdtDbtInd>
            <RltdPties><Cdtr><Nm>SWIGGY ORDER</Nm></Cdtr></RltdPties>
            <RmtInf><Ustrd>Order 4471</Ustrd></RmtInf>
          </TxDtls>
        </NtryDtls>
      </Ntry>
      <Ntry>
        <Amt Ccy="INR">75000.00</Amt>
        <CdtDbtInd>CRDT</CdtDbtInd>
        <Sts>BOOK</Sts>
        <BookgDt><Dt>2026-05-20</Dt></BookgDt>
        <NtryDtls>
          <TxDtls>
            <Amt Ccy="INR">75000.00</Amt>
            <CdtDbtInd>CRDT</CdtDbtInd>
            <RltdPties><Dbtr><Nm>ACME CORP</Nm></Dbtr></RltdPties>
            <RmtInf><Ustrd>Salary May</Ustrd></RmtInf>
          </TxDtls>
        </NtryDtls>
      </Ntry>
      <Ntry>
        <Amt Ccy="INR">99.99</Amt>
        <CdtDbtInd>DBIT</CdtDbtInd>
        <Sts>BOOK</Sts>
        <BookgDt><Dt>2026-05-22T09:30:00+05:30</Dt></BookgDt>
        <AddtlNtryInf>BANK CHARGES</AddtlNtryInf>
      </Ntry>
    </Stmt>
  </BkToCstmrStmt>
</Document>
`;

// A camt.052 statement: same shape plus the period's opening and closing
// balances, which is what a future reconciliation would check the ledger
// against. The liability account reports CLBD as a DBIT balance, because a
// credit card's closing balance is an amount owed.
export const CAMT_052_CARD = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.052.001.02">
  <BkToCstmrStmt>
    <GrpHdr><MsgId>MSG-2</MsgId><CreDtTm>2026-05-31T12:00:00</CreDtTm></GrpHdr>
    <Stmt>
      <Id>STMT-0002</Id>
      <Acct>
        <Id><Othr><Id>XXXX5678</Id></Othr></Id>
        <Ccy>INR</Ccy>
        <Nm>CARD HOLDER</Nm>
      </Acct>
      <Bal>
        <Tp><CdOrPrtry><Cd>OPBD</Cd></CdOrPrtry></Tp>
        <Amt Ccy="INR">40649.25</Amt>
        <CdtDbtInd>DBIT</CdtDbtInd>
        <Dt><Dt>2026-05-01</Dt></Dt>
      </Bal>
      <Bal>
        <Tp><CdOrPrtry><Cd>CLBD</Cd></CdOrPrtry></Tp>
        <Amt Ccy="INR">38150.25</Amt>
        <CdtDbtInd>DBIT</CdtDbtInd>
        <Dt><Dt>2026-05-31</Dt></Dt>
      </Bal>
      <Ntry>
        <Amt Ccy="INR">2499.00</Amt>
        <CdtDbtInd>DBIT</CdtDbtInd>
        <Sts>BOOK</Sts>
        <BookgDt><Dt>2026-05-03</Dt></BookgDt>
        <NtryDtls><TxDtls>
          <RltdPties><Cdtr><Nm>AMAZON</Nm></Cdtr></RltdPties>
        </TxDtls></NtryDtls>
      </Ntry>
      <Ntry>
        <Amt Ccy="INR">150.00</Amt>
        <CdtDbtInd>CRDT</CdtDbtInd>
        <Sts>BOOK</Sts>
        <BookgDt><Dt>2026-05-09</Dt></BookgDt>
        <NtryDtls><TxDtls>
          <RltdPties><Dbtr><Nm>AMAZON</Nm></Dbtr></RltdPties>
        </TxDtls></NtryDtls>
      </Ntry>
    </Stmt>
  </BkToCstmrStmt>
</Document>
`;

// Two accounts in one camt file, which ISO 20022 allows and some banks produce
// when a customer downloads a whole period.
export const CAMT_TWO_ACCOUNTS = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.08">
  <BkToCstmrStmt>
    <GrpHdr><MsgId>MSG-3</MsgId></GrpHdr>
    <Stmt>
      <Id>A</Id>
      <Acct><Id><Othr><Id>AAA111</Id></Othr></Id><Ccy>INR</Ccy></Acct>
      <Ntry><Amt Ccy="INR">10.00</Amt><CdtDbtInd>DBIT</CdtDbtInd><Sts>BOOK</Sts>
        <BookgDt><Dt>2026-05-02</Dt></BookgDt>
        <NtryDtls><TxDtls><RltdPties><Cdtr><Nm>COFFEE SHOP</Nm></Cdtr></RltdPties></TxDtls></NtryDtls>
      </Ntry>
    </Stmt>
    <Stmt>
      <Id>B</Id>
      <Acct><Id><Othr><Id>BBB222</Id></Othr></Id><Ccy>INR</Ccy></Acct>
      <Ntry><Amt Ccy="INR">20.00</Amt><CdtDbtInd>CRDT</CdtDbtInd><Sts>BOOK</Sts>
        <BookgDt><Dt>2026-04-15</Dt></BookgDt>
        <NtryDtls><TxDtls><RltdPties><Dbtr><Nm>REFUND CO</Nm></Dbtr></RltdPties></TxDtls></NtryDtls>
      </Ntry>
    </Stmt>
  </BkToCstmrStmt>
</Document>
`;

// Entries the reader must refuse rather than invent, plus one pending entry
// that must be reported but not booked.
export const CAMT_MALFORMED = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <BkToCstmrStmt>
    <Stmt>
      <Id>M</Id>
      <Acct><Id><Othr><Id>ERR1</Id></Othr></Id><Ccy>INR</Ccy></Acct>
      <Ntry><CdtDbtInd>DBIT</CdtDbtInd><Sts>BOOK</Sts><BookgDt><Dt>2026-05-01</Dt></BookgDt>
        <NtryDtls><TxDtls><RltdPties><Cdtr><Nm>NO AMOUNT</Nm></Cdtr></RltdPties></TxDtls></NtryDtls>
      </Ntry>
      <Ntry><Amt Ccy="INR">10.005</Amt><CdtDbtInd>DBIT</CdtDbtInd><Sts>BOOK</Sts><BookgDt><Dt>2026-05-01</Dt></BookgDt>
        <NtryDtls><TxDtls><RltdPties><Cdtr><Nm>THREE DECIMALS</Nm></Cdtr></RltdPties></TxDtls></NtryDtls>
      </Ntry>
      <Ntry><Amt Ccy="INR">10.00</Amt><CdtDbtInd>DBIT</CdtDbtInd><Sts>BOOK</Sts>
        <NtryDtls><TxDtls><RltdPties><Cdtr><Nm>NO DATE</Nm></Cdtr></RltdPties></TxDtls></NtryDtls>
      </Ntry>
      <Ntry><Amt Ccy="INR">10.00</Amt><CdtDbtInd>DBIT</CdtDbtInd><Sts>BOOK</Sts><BookgDt><Dt>2026-05-01</Dt></BookgDt>
      </Ntry>
      <Ntry><Amt Ccy="INR">99.00</Amt><CdtDbtInd>DEBIT</CdtDbtInd><Sts>BOOK</Sts><BookgDt><Dt>2026-05-01</Dt></BookgDt>
        <NtryDtls><TxDtls><RltdPties><Cdtr><Nm>BAD DIRECTION</Nm></Cdtr></RltdPties></TxDtls></NtryDtls>
      </Ntry>
      <Ntry><Amt Ccy="INR">500.00</Amt><CdtDbtInd>DBIT</CdtDbtInd><Sts>PDNG</Sts><BookgDt><Dt>2026-06-01</Dt></BookgDt>
        <NtryDtls><TxDtls><RltdPties><Cdtr><Nm>PENDING HOTEL</Nm></Cdtr></RltdPties></TxDtls></NtryDtls>
      </Ntry>
      <Ntry><Amt Ccy="INR">42.00</Amt><CdtDbtInd>DBIT</CdtDbtInd><Sts>BOOK</Sts><BookgDt><Dt>2026-05-01</Dt></BookgDt>
        <NtryDtls><TxDtls><RltdPties><Cdtr><Nm>GOOD ROW</Nm></Cdtr></RltdPties></TxDtls></NtryDtls>
      </Ntry>
    </Stmt>
  </BkToCstmrStmt>
</Document>
`;

// A statement that parsed cleanly but holds no entries.
export const CAMT_NO_ENTRIES = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <BkToCstmrStmt>
    <Stmt>
      <Id>EMPTY</Id>
      <Acct><Id><Othr><Id>ZERO</Id></Othr></Id><Ccy>INR</Ccy></Acct>
    </Stmt>
  </BkToCstmrStmt>
</Document>
`;

// A camt file namespaced with a prefix rather than as a default namespace, which
// some exporters do. Local-name lookup has to survive it.
export const CAMT_PREFIXED = `<?xml version="1.0" encoding="UTF-8"?>
<c53:Document xmlns:c53="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <c53:BkToCstmrStmt>
    <c53:Stmt>
      <c53:Id>P</c53:Id>
      <c53:Acct><c53:Id><c53:Othr><c53:Id>PFX1</c53:Id></c53:Othr></c53:Id><c53:Ccy>INR</c53:Ccy></c53:Acct>
      <c53:Ntry><c53:Amt Ccy="INR">5.00</c53:Amt><c53:CdtDbtInd>DBIT</c53:CdtDbtInd><c53:Sts>BOOK</c53:Sts>
        <c53:BookgDt><c53:Dt>2026-05-05</c53:Dt></c53:BookgDt>
        <c53:NtryDtls><c53:TxDtls><c53:RltdPties><c53:Cdtr><c53:Nm>PREFIXED PAYEE</c53:Nm></c53:Cdtr></c53:RltdPties></c53:TxDtls></c53:NtryDtls>
      </c53:Ntry>
    </c53:Stmt>
  </c53:BkToCstmrStmt>
</c53:Document>
`;

// Well-formed XML that is not a statement at all.
export const CAMT_WRONG_MESSAGE = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pain.001.001.03">
  <CstmrPmtStsRpt><GrpHdr><MsgId>PAY-1</MsgId></GrpHdr></CstmrPmtStsRpt>
</Document>
`;

// Not well-formed XML.
export const CAMT_BROKEN_XML = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <BkToCstmrStmt><Stmt><Acct><Id><Othr><Id>X</Id></Othr>
</Document>
`;
