import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { Spinner } from '@/components/Spinner';

// /errors → /exceptions. The merged Exceptions page subsumes
// the former Errors / Anomalies / Problems URLs into a single
// triage surface: inbox at top, alert problems next, anomaly
// streams below.
export default function ErrorsRedirectPage() {
  const navigate = useNavigate();
  useEffect(() => { navigate('/exceptions', { replace: true }); }, [navigate]);
  return <Spinner />;
}
