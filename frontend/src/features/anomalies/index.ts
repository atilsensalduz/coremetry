// Public surface of the merged exceptions feature. Anyone outside this
// folder imports from here, so internal re-organisation
// (splitting LogPatternsSection out, hoisting helpers) doesn't
// break consumers. The folder kept its anomalies/ name since the
// containing module includes anomaly streams alongside the
// exception inbox + problems table — renaming the folder is
// purely cosmetic churn for one extra import path layer.
export { default } from './AnomaliesPage';
export { default as ExceptionsPage } from './AnomaliesPage';
