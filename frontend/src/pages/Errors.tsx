import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { Spinner } from '@/components/Spinner';

// /errors → /problems. The merged Problems page subsumes the
// former Errors / Anomalies URLs into a single triage surface:
// assignable exception inbox at top, alert-rule firings next,
// anomaly streams below.
export default function ErrorsRedirectPage() {
  const navigate = useNavigate();
  useEffect(() => { navigate('/problems', { replace: true }); }, [navigate]);
  return <Spinner />;
}
