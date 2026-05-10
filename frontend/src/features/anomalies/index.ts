// Public surface of the merged Problems feature. Anyone outside this
// folder imports from here, so internal re-organisation
// (splitting LogPatternsSection out, hoisting helpers) doesn't
// break consumers. The folder kept its anomalies/ name even though
// the user-facing page is "Problems" — renaming the folder is
// purely cosmetic churn for one extra import path layer.
export { default } from './AnomaliesPage';
export { default as ProblemsPage } from './AnomaliesPage';
