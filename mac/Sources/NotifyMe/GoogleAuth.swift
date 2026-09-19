import Foundation
import Crypto
import _CryptoExtras

/// The fields we need out of a downloaded Google service-account key
/// file (Cloud Console -> IAM & Admin -> Service Accounts -> Keys ->
/// Add key -> JSON). Share the target spreadsheet with `clientEmail` as
/// an Editor — the same idea as sharing a Notion database with a Notion
/// integration.
struct GoogleServiceAccountKey: Decodable {
    let clientEmail: String
    let privateKey: String
    let tokenUri: String

    enum CodingKeys: String, CodingKey {
        case clientEmail = "client_email"
        case privateKey = "private_key"
        case tokenUri = "token_uri"
    }
}

enum GoogleAuthError: Error, LocalizedError {
    case tokenRequestFailed(Int, String)
    var errorDescription: String? {
        switch self {
        case .tokenRequestFailed(let code, let body):
            return "Google token request failed (\(code)): \(body)"
        }
    }
}

/// Mints and caches OAuth2 access tokens for a service account via the
/// JWT bearer flow (RFC 7523) — the standard way a server (or, here, a
/// personal local app) authenticates as itself with no interactive
/// consent screen. Signing is RS256 over a JWT assertion; Foundation has
/// no RSA signing, so this uses swift-crypto's _CryptoExtras.
///
/// One shared cache (keyed by service account email) so every
/// GoogleSheetsStore instance reuses a still-valid token instead of
/// minting a fresh one on every card tap.
actor GoogleTokenCache {
    static let shared = GoogleTokenCache()

    private var tokens: [String: (token: String, expiry: Date)] = [:]

    func token(for key: GoogleServiceAccountKey, scope: String) async throws -> String {
        let cacheKey = key.clientEmail
        if let cached = tokens[cacheKey], Date() < cached.expiry {
            return cached.token
        }

        let (token, expiry) = try await Self.mintToken(for: key, scope: scope)
        tokens[cacheKey] = (token, expiry)
        return token
    }

    private static func mintToken(for key: GoogleServiceAccountKey, scope: String) async throws -> (String, Date) {
        let now = Date()
        let expiresAt = now.addingTimeInterval(3600)

        let header: [String: Any] = ["alg": "RS256", "typ": "JWT"]
        let claims: [String: Any] = [
            "iss": key.clientEmail,
            "scope": scope,
            "aud": key.tokenUri,
            "iat": Int(now.timeIntervalSince1970),
            "exp": Int(expiresAt.timeIntervalSince1970),
        ]

        let signingInput = try "\(base64URL(header)).\(base64URL(claims))"
        let privateKey = try _RSA.Signing.PrivateKey(pemRepresentation: key.privateKey)
        let signature = try privateKey.signature(for: Data(signingInput.utf8), padding: .insecurePKCS1v1_5)
        let jwt = "\(signingInput).\(base64URLEncode(signature.rawRepresentation))"

        var request = URLRequest(url: URL(string: key.tokenUri)!)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")

        var form = URLComponents()
        form.queryItems = [
            URLQueryItem(name: "grant_type", value: "urn:ietf:params:oauth:grant-type:jwt-bearer"),
            URLQueryItem(name: "assertion", value: jwt),
        ]
        request.httpBody = form.percentEncodedQuery?.data(using: .utf8)

        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            let status = (response as? HTTPURLResponse)?.statusCode ?? -1
            throw GoogleAuthError.tokenRequestFailed(status, String(data: data, encoding: .utf8) ?? "")
        }

        struct TokenResponse: Decodable {
            let accessToken: String
            let expiresIn: Int
            enum CodingKeys: String, CodingKey {
                case accessToken = "access_token"
                case expiresIn = "expires_in"
            }
        }
        let decoded = try JSONDecoder().decode(TokenResponse.self, from: data)
        // Refresh a minute early rather than racing the server's own expiry.
        return (decoded.accessToken, now.addingTimeInterval(TimeInterval(decoded.expiresIn - 60)))
    }
}

private func base64URL(_ jsonObject: [String: Any]) throws -> String {
    base64URLEncode(try JSONSerialization.data(withJSONObject: jsonObject))
}

func base64URLEncode<D: DataProtocol>(_ data: D) -> String {
    Data(data).base64EncodedString()
        .replacingOccurrences(of: "+", with: "-")
        .replacingOccurrences(of: "/", with: "_")
        .replacingOccurrences(of: "=", with: "")
}
