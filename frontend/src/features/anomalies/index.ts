// Public surface of the anomalies feature. Anyone outside this
// folder imports from here, so internal re-organisation
// (splitting LogPatternsSection out, hoisting helpers) doesn't
// break consumers.
export { default } from './AnomaliesPage';
export { default as AnomaliesPage } from './AnomaliesPage';
