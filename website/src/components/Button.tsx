import React from 'react';

interface ButtonProps {
  to?: string;
  href?: string;
  children: React.ReactNode;
  variant?: 'primary' | 'secondary';
}

export default function Button({ to, href, children, variant = 'primary' }: ButtonProps) {
  const link = to || href || '#';
  return (
    <a 
      href={link} 
      className={`button button--${variant}`}
      style={{ marginRight: '0.5rem', marginBottom: '0.5rem' }}
    >
      {children}
    </a>
  );
}
