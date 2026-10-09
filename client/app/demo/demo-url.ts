// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

const preferredParameterOrder = ['modal', 'update', 'deployment', 'hash'];

export function canonicalDemoSearchParams(params: URLSearchParams): URLSearchParams {
  const canonical = new URLSearchParams();

  preferredParameterOrder.forEach(key => {
    params.getAll(key).forEach(value => canonical.append(key, value));
  });
  params.forEach((value, key) => {
    if (!preferredParameterOrder.includes(key)) {
      canonical.append(key, value);
    }
  });

  return canonical;
}

