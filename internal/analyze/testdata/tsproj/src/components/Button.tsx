import React from 'react';

type Props = { label: string };

export const Button = ({ label }: Props) => {
  const api = import.meta.env.VITE_API_URL;
  return <button data-api={api}>{label}</button>;
};
